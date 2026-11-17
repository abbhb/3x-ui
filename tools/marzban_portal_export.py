#!/usr/bin/env python3
"""Stage 3: export a frozen Marzban SQLite copy to a private, atomic import bundle.

Python 3.11+, standard library; never connects to a running panel or target DB.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import time

from marzban_export import (MigrationError, require, integer, snapshot, table_names,
                            read_json, encode, decode, digest, convert, route_bindings,
                            allocation_snapshot)

SCHEMA = "marzban-portal-to-3x-ui/v1"
MAX_MONEY = 9_000_000_000_000_000


def stamp(value):
    if value is None or value == "":
        return 0
    require(isinstance(value, str), "invalid source UTC datetime")
    try:
        dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
        dt = dt.replace(tzinfo=timezone.utc) if dt.tzinfo is None else dt.astimezone(timezone.utc)
        result = int(dt.timestamp() * 1000)
    except (ValueError, OverflowError):
        raise MigrationError("invalid source UTC datetime") from None
    return integer(result, "source datetime")


def rows(db, table, tables):
    # Table names only come from fixed calls below, never operator input.
    return [dict(r) for r in db.execute('SELECT * FROM "' + table + '" ORDER BY id')] if table in tables else []


def j(value, default):
    return default if value is None else decode(value, "source metadata")


def required_bool(value):
    require(type(value) is int and value in (0, 1), "invalid source boolean")
    return bool(value)


def money(value):
    return integer(value, "money", maximum=MAX_MONEY)


def build(db, core, mapping, inventory, route_map, target_hosts, now=None, archive_unmanaged_disabled=False):
    tables = table_names(db)
    require({"portal_accounts", "portal_subscriptions", "portal_purchases", "wallet_transactions",
             "subscription_plans", "portal_invitation_codes", "portal_invitation_uses",
             "portal_ip_blocks", "portal_security_settings", "mgma_settings", "jwt"} <= tables,
            "stage 3 requires the complete source commerce and MGMA schema")
    source_users = rows(db, "users", tables)
    linked = {a["user_id"] for a in rows(db, "portal_accounts", tables) if a["user_id"] is not None}
    pending = {p["user_id"] for p in rows(db, "next_plans", tables)}
    archived = {u["id"] for u in source_users if archive_unmanaged_disabled and u["status"].lower() == "disabled" and u["id"] not in linked and u["id"] not in pending}
    selected = [u["id"] for u in source_users if u["id"] not in archived]
    require(selected, "no managed or active source users to migrate")
    clients, identities = convert(db, core, mapping, inventory, user_ids=selected, route_map=route_map,
                                 target_hosts=target_hosts, traffic_policy="preserve-total-as-download",
                                 allow_next_plans=True, now=now)
    route_bindings(route_map, target_hosts, mapping)
    for c in clients:
        c["client"]["requireMGMA"] = True
    users = {u["id"]: u for u in source_users if u["id"] not in archived}
    native_by_user = {i["legacy_user_id"]: c for i, c in zip(identities, clients)}
    reserved = sorted(u["id"] for u in source_users)
    def grant(tags, selections=None):
        require(isinstance(tags, list) and all(isinstance(t, str) and t in mapping for t in tags),
                "plan references an unmapped source inbound")
        host_ids = set()
        for tag in tags:
            require(tag in route_map, "plan inbound lacks an explicit host projection")
            selected = (selections or {}).get(tag)
            selected = list(route_map[tag]) if selected is None else selected
            require(isinstance(selected, list) and set(selected) <= route_map[tag].keys(), "plan selects an unmapped source route")
            for key in selected:
                host_ids.update(route_map[tag][key])
        return sorted({i for t in tags for i in mapping[t]}), sorted(host_ids)
    external = rows(db, "plan_external_source_selections", tables)
    external_plans = {r["plan_id"] for r in external}
    plans = []
    source_plans = {r["id"]: r for r in rows(db, "subscription_plans", tables)}
    for r in source_plans.values():
        require(r["currency"] == "CNY", "unsupported source currency")
        inbound_ids, host_ids = grant(j(r["inbound_tags"], []), j(r.get("local_node_selections"), {}))
        pending = r["id"] in external_plans
        metadata = {k: j(r.get(k), {}) for k in ("group_members", "local_node_selections")}
        # Rules and external entitlements are preserved for stage 4, never charged as a partial plan.
        metadata["external_selections"] = [x for x in external if x["plan_id"] == r["id"]]
        plans.append(dict(id=integer(r["id"], "plan ID", minimum=1), name=r["name"], description=r.get("description") or "",
                          price_minor=money(r["price_minor"]), currency="CNY", duration_days=integer(r["duration_days"], "duration", minimum=1, maximum=3650),
                          data_limit=integer(r["data_limit"], "data limit", maximum=10**18), inbound_ids=inbound_ids,
                          host_ids=host_ids, is_visible=required_bool(r["is_visible"]) and not pending,
                          compatibility_pending=pending, legacy_metadata=json.dumps(metadata, ensure_ascii=False),
                          created_at=stamp(r["created_at"]), updated_at=stamp(r["updated_at"])))
    accounts = []
    seen_names = set()
    source_accounts = {r["id"]: r for r in rows(db, "portal_accounts", tables)}
    for r in source_accounts.values():
        name = r["username"].strip().lower()
        require(re.fullmatch(r"[a-z0-9_@.-]{3,32}", name) and name not in seen_names, "unsupported/duplicate case-folded portal username")
        seen_names.add(name)
        require(re.fullmatch(r"\$2[aby]\$\d\d\$[./A-Za-z0-9]{53}", r["hashed_password"]), "unsupported portal password hash")
        require(r["user_id"] is None or r["user_id"] in users, "account references a missing proxy user")
        accounts.append(dict(id=r["id"], username=name, password_hash=r["hashed_password"], wallet_balance_minor=money(r["wallet_balance_minor"]),
                             is_active=required_bool(r["is_active"]), client_id=r["user_id"], created_at=stamp(r["created_at"]), updated_at=stamp(r["updated_at"])))
    purchases = []
    for r in rows(db, "portal_purchases", tables):
        require(r["account_id"] in source_accounts and r["plan_id"] in source_plans and r["currency"] == "CNY", "invalid purchase reference/currency")
        original_tags = j(r["inbound_tags"], [])
        require(isinstance(original_tags, list) and len(original_tags) <= 128 and all(isinstance(t, str) and 0 < len(t) <= 256 and not any(c in t for c in "\r\n\x00") for t in original_tags), "invalid historical inbound tags")
        retired_tags = [t for t in original_tags if t not in mapping]
        inbounds, hosts = grant([t for t in original_tags if t in mapping])
        purchases.append(dict(id=r["id"], account_id=r["account_id"], plan_id=r["plan_id"], plan_name=r["plan_name"],
                              price_minor=money(r["amount_minor"]), amount_minor=money(r["amount_minor"]), currency="CNY", duration_days=r["duration_days"],
                              data_limit=integer(r["data_limit"], "purchase data limit", maximum=10**18), inbound_ids=inbounds, host_ids=hosts,
                              balance_before_minor=money(r["balance_before_minor"]), balance_after_minor=money(r["balance_after_minor"]),
                              kind=r["kind"], actor_admin=r["actor_admin"] or "", idempotency_key=r["idempotency_key"] or f"import-purchase-{r['id']}",
                              effective_expires_at=stamp(r["effective_expires_at"]), created_at=stamp(r["created_at"])))
        if retired_tags:
            purchases[-1]["legacy_inbound_tags"] = original_tags
    subscriptions = []
    for r in rows(db, "portal_subscriptions", tables):
        require(r["account_id"] in source_accounts and r["plan_id"] in source_plans, "invalid current subscription reference")
        require(source_accounts[r["account_id"]]["user_id"] in users or (source_accounts[r["account_id"]]["user_id"] is None and (stamp(r["disabled_at"]) > 0 or 0 < stamp(r["expires_at"]) <= int(time.time() if now is None else now)*1000)), "active subscription without proxy user")
        # Current grants follow the current source plan; historical commercial fields stay unchanged.
        plan = next(p for p in plans if p["id"] == r["plan_id"])
        subscriptions.append(dict(account_id=r["account_id"], plan_id=r["plan_id"], plan_name=r["plan_name"], price_minor=money(r["price_paid_minor"]),
                                  price_paid_minor=money(r["price_paid_minor"]), currency=r["currency"], duration_days=r["duration_days"], data_limit=r["data_limit"],
                                  inbound_ids=plan["inbound_ids"], host_ids=plan["host_ids"], starts_at=stamp(r["starts_at"]), expires_at=stamp(r["expires_at"]),
                                  purchased_at=stamp(r["purchased_at"]), disabled_at=stamp(r["disabled_at"])))
    wallet, balances = [], {}
    purchase_ids = {r["id"] for r in purchases}
    for r in rows(db, "wallet_transactions", tables):
        require(r["account_id"] in source_accounts and (r["purchase_id"] is None or r["purchase_id"] in purchase_ids), "invalid wallet reference")
        amount = integer(r["amount_minor"], "ledger amount", minimum=-MAX_MONEY, maximum=MAX_MONEY)
        require(amount != 0 and ((r["kind"] == "admin_credit" and amount > 0) or (r["kind"] == "purchase_debit" and amount < 0)), "invalid ledger direction")
        after = money(r["balance_after_minor"])
        # The source creates all wallets at zero. No unexplained opening credit is invented.
        require(balances.get(r["account_id"], 0) + amount == after, "wallet ledger continuity failure")
        balances[r["account_id"]] = after
        wallet.append(dict(id=r["id"], account_id=r["account_id"], kind=r["kind"], amount_minor=amount, balance_after_minor=after,
                           actor_admin=r["actor_admin"] or "", purchase_id=r["purchase_id"], note=r["note"] or "",
                           idempotency_key=r["idempotency_key"] or f"import-wallet-{r['id']}", created_at=stamp(r["created_at"])))
    require(all(balances.get(a["id"], 0) == a["wallet_balance_minor"] for a in accounts), "wallet does not reconcile to the account balance")
    invitations = [dict(id=r["id"], digest=r["code_digest"], prefix=r["code_prefix"], note=r["note"], starts_at=stamp(r["valid_from"]),
                        expires_at=stamp(r["expires_at"]), max_uses=r["max_uses"] or 0, use_count=r["use_count"],
                        disabled_at=0 if r["is_active"] else stamp(r["updated_at"]), actor_admin=r["created_by"], created_at=stamp(r["created_at"]))
                   for r in rows(db, "portal_invitation_codes", tables)]
    uses = [dict(id=r["id"], invitation_id=r["invitation_id"], account_id=r["account_id"], source_ip=r["source_ip"], created_at=stamp(r["used_at"]))
            for r in rows(db, "portal_invitation_uses", tables)]
    blocks = [dict(id=r["id"], network=r["network"], reason=r["reason"], source=r["source"], actor_admin=r["created_by"],
                   expires_at=stamp(r["expires_at"]), revoked_at=stamp(r["revoked_at"]) or (0 if r["is_active"] else stamp(r["updated_at"])), created_at=stamp(r["created_at"]))
              for r in rows(db, "portal_ip_blocks", tables)]
    attempts = [dict(id=r["id"], source_ip=r["source_ip"], kind=r["kind"], failure_count=r["failure_count"], window_start=stamp(r["window_started_at"]), updated_at=stamp(r["last_failed_at"]))
                for r in rows(db, "portal_security_attempts", tables)]
    require(len(attempts) <= 10000, "source security-attempt key space exceeds its bound")
    mgma = rows(db, "mgma_settings", tables)
    require(len(mgma) == 1, "missing singleton MGMA settings")
    mgma = mgma[0]
    security = rows(db, "portal_security_settings", tables)
    require(len(security) == 1, "missing singleton portal security settings")
    security = security[0]
    settings = {k: security[k] for k in ("login_failure_limit", "registration_failure_limit", "login_window_seconds", "registration_window_seconds", "auto_block_seconds")}
    settings.update(enabled=False, public_url="", mode=mgma["mode"], ttl_seconds=mgma["ttl_seconds"], single_use=required_bool(mgma["single_use"]),
                    source_mode=mgma["source_mode"], custom_cidrs=j(mgma["custom_cidrs"], []), auto_block_enabled=required_bool(security["auto_block_enabled"]))
    access, legacy = [], []
    for uid, u in users.items():
        access.append(dict(client_id=uid, manually_disabled=u["status"].lower() == "disabled", digest=u["sub_access_token_digest"] or "", issued_at=stamp(u["sub_access_issued_at"]),
                           expires_at=stamp(u["sub_access_expires_at"]), consumed_at=stamp(u["sub_access_consumed_at"]), generation=1))
        legacy.append(dict(client_id=uid, username=u["username"], created_at=stamp(u["created_at"]), revoked_at=stamp(u["sub_revoked_at"])))
    next_plans = [dict(client_id=r["user_id"], data_limit=r["data_limit"], expire=r["expire"] or 0,
                       add_remaining_traffic=required_bool(r["add_remaining_traffic"]), fire_on_either=required_bool(r["fire_on_either"]),
                       paused=users[r["user_id"]]["status"].lower() == "disabled") for r in rows(db, "next_plans", tables)]
    usage = [dict(id=r["id"], client_id=r["user_id"], used_traffic=integer(r["used_traffic_at_reset"], "historical traffic"), reset_at=stamp(r["reset_at"])) for r in rows(db, "user_usage_logs", tables) if r["user_id"] in users]
    jwt = rows(db, "jwt", tables)
    require(len(jwt) == 1 and len(jwt[0]["secret_key"]) >= 32, "missing legacy subscription signing key")
    pepper = os.environ.get("MGMA_TOKEN_PEPPER", "")
    if any(a["digest"] for a in access):
        require(len(pepper.encode()) >= 32, "existing MGMA grants require source MGMA_TOKEN_PEPPER for its fingerprint")
    # Ciphertexts and references remain private; no plaintext URLs or signing keys are exported.
    future = {t: rows(db, t, tables) for t in ("external_subscription_sources", "external_subscription_nodes", "plan_external_source_selections",
              "subscription_rules", "subscription_group_order")}
    future["source_commerce_snapshot"] = {t: rows(db, t, tables) for t in (
        "portal_accounts", "subscription_plans", "portal_subscriptions", "portal_purchases", "wallet_transactions",
        "portal_invitation_codes", "portal_invitation_uses", "portal_ip_blocks", "portal_security_attempts",
        "portal_security_settings", "mgma_settings", "user_usage_logs")}
    if archived:
        proxies = [p for p in rows(db, "proxies", tables) if p["user_id"] in archived]
        proxy_ids = {p["id"] for p in proxies}
        future["archived_legacy_users"] = {
            "reason": "explicitly retained on the legacy service: disabled, no portal account, no pending plan",
            "users": [u for u in source_users if u["id"] in archived], "proxies": proxies,
            "exclude_inbounds_association": [dict(r) for r in db.execute("SELECT * FROM exclude_inbounds_association ORDER BY proxy_id,inbound_tag") if r["proxy_id"] in proxy_ids],
            "user_usage_logs": [r for r in rows(db, "user_usage_logs", tables) if r["user_id"] in archived],
            "node_user_usages": [r for r in rows(db, "node_user_usages", tables) if r["user_id"] in archived],
        }
    bundle = dict(schema=SCHEMA, clients=clients, reserved_ids=reserved, accounts=accounts, plans=plans, subscriptions=subscriptions, purchases=purchases,
                  wallet=wallet, invitations=invitations, invitation_uses=uses, ip_blocks=blocks, security_attempts=attempts, settings=settings,
                  access=access, legacy_users=legacy, next_plans=next_plans, usage_history=usage,
                  legacy_key_sha256=digest(jwt[0]["secret_key"].encode()), pepper_sha256=digest(pepper.encode()) if pepper else "")
    return bundle, identities, future


def export(args):
    require(getattr(args, "traffic_policy", None) == "preserve-total-as-download", "stage 3 requires explicit --traffic-policy preserve-total-as-download")
    core, core_hash = read_json(args.core, "source core")
    mapping, map_hash = read_json(args.inbound_map, "inbound map")
    inventory, inventory_hash = read_json(args.target_inbounds, "target inbounds")
    routes, route_hash = read_json(args.route_map, "route map")
    hosts, host_hash = read_json(args.target_hosts, "target hosts")
    with snapshot(args.database) as (db, db_hash):
        bundle, identities, future = build(db, core, mapping, inventory, routes, hosts, archive_unmanaged_disabled=getattr(args, "archive_unmanaged_disabled", False))
        template_hash = None
        if getattr(args, "stage4", False):
            from marzban_subscription_export import extend
            require(args.clash_template, "stage 4 requires the frozen Clash/Mihomo template")
            names = read_json(args.tls_name_map, "TLS names")[0] if args.tls_name_map else None
            template_hash = extend(db, bundle, core, mapping, routes, hosts, args.clash_template, names)
    members = {"portal.json": encode(bundle), "identities.json": encode(identities), "stage4-private.json": encode(future)}
    reserved = set(bundle["reserved_ids"])
    allocations = []
    for spec in args.ipv6_allocation:
        namespace, sep, path = spec.partition("=")
        require(sep and re.fullmatch(r"[A-Za-z0-9_-]{1,64}", namespace), "allocation must be NAME=SNAPSHOT.sqlite3")
        name = f"allocations/{namespace}.sqlite3"
        require(name not in members, "duplicate allocation namespace")
        blob, meta = allocation_snapshot(path, reserved)
        reserved.update(meta["orphan_legacy_user_ids"])
        members[name] = blob
        allocations.append(dict(file=name, **meta))
    bundle["reserved_ids"] = sorted(reserved)
    members["portal.json"] = encode(bundle)
    manifest = dict(schema=SCHEMA, ready_for_cutover=False, source_sha256=db_hash, client_count=len(bundle["clients"]), account_count=len(bundle["accounts"]),
                    sources=dict(core=core_hash, inbound_map=map_hash, target_inbounds=inventory_hash, route_map=route_hash, target_hosts=host_hash),
                    allocations=allocations, files={name: digest(blob) for name, blob in members.items()})
    if template_hash:
        manifest["sources"]["clash_template"] = template_hash
    root = Path(args.output)
    root.mkdir(mode=0o700)
    try:
        for name, blob in {**members, "manifest.json": encode(manifest)}.items():
            path = root / name
            path.parent.mkdir(mode=0o700, exist_ok=True)
            with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as f:
                f.write(blob)
    except BaseException:
        shutil.rmtree(root)
        raise
    return manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for field in ("database", "core", "inbound-map", "target-inbounds", "route-map", "target-hosts", "output"):
        parser.add_argument("--" + field, required=True)
    parser.add_argument("--traffic-policy", required=True, choices=["preserve-total-as-download"])
    parser.add_argument("--ipv6-allocation", action="append", default=[])
    parser.add_argument("--archive-unmanaged-disabled", action="store_true", help="retain disabled users without portal accounts or pending plans in the private legacy archive, reserving all identities")
    parser.add_argument("--stage4", action="store_true", help="include external ciphertext, native Hosts, policy and archived route metadata")
    parser.add_argument("--clash-template")
    parser.add_argument("--tls-name-map")
    args = parser.parse_args(argv)
    try:
        result = export(args)
        print(f"Private stage 3 bundle prepared: {result['client_count']} clients, {result['account_count']} accounts; not ready for cutover")
    except MigrationError as error:
        print("Stage 3 export rejected: " + str(error), file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, TypeError):
        print("Stage 3 export rejected: check schema, grants, integer ledger and snapshot inputs; no credential values are logged", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
