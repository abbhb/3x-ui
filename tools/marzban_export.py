#!/usr/bin/env python3
"""Offline Marzban -> 3x-ui preparation. No network calls or target DB writes.

The bundle contains credentials. See docs/migration/MARZBAN.zh-CN.md.
Python 3.11+, standard library only.
"""

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import sys
import time
import uuid


SCHEMA = "marzban-to-3x-ui/v1"
VISION = "xtls-rprx-vision"
MAX_INT64 = (1 << 63) - 1


class MigrationError(Exception):
    """Diagnostics must identify fields, never include credential values."""


def require(condition, message):
    if not condition:
        raise MigrationError(message)


def integer(value, field, maximum=MAX_INT64, minimum=0):
    require(type(value) is int and minimum <= value <= maximum,
            f"invalid {field}: expected an integer in range")
    return value


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encode(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()


def decode(data, field):
    try:
        return json.loads(data)
    except (ValueError, TypeError, UnicodeError):
        raise MigrationError(f"invalid JSON in {field}") from None


def read_json(path, field):
    data = Path(path).read_bytes()
    return decode(data, field), digest(data)


@contextmanager
def snapshot(path):
    path = Path(path).resolve(strict=True)
    wal = Path(str(path) + "-wal")
    require(not wal.exists() or wal.stat().st_size == 0,
            "nonempty SQLite WAL: create a standalone backup before exporting")
    before = digest(path.read_bytes())
    db = sqlite3.connect(path.as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    try:
        db.execute("PRAGMA query_only=ON")
        db.execute("BEGIN")
        require(db.execute("PRAGMA quick_check").fetchone()[0] == "ok",
                "SQLite snapshot failed integrity check")
        yield db, before
        require(digest(path.read_bytes()) == before, "SQLite snapshot changed during export")
        require(not wal.exists() or wal.stat().st_size == 0,
                "SQLite WAL changed during export")
    finally:
        db.close()


def table_names(db):
    return {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}


def native_identifier(value, field):
    require(isinstance(value, str) and value and value not in (".", "..") and
            not any(c in "/\\" or ord(c) < 32 or ord(c) == 127 or c.isspace() for c in value),
            f"invalid {field} for native client identifier")
    return value


def object_json(value, field):
    result = decode(value, field)
    require(isinstance(result, dict), f"invalid {field}: expected an object")
    return result


def supports_vision(inbound):
    stream = inbound.get("streamSettings") or {}
    require(isinstance(stream, dict), "invalid source streamSettings")
    transport = stream.get(f"{stream.get('network', 'tcp')}Settings") or {}
    require(isinstance(transport, dict), "invalid transport settings")
    header = transport.get("header") or {}
    require(isinstance(header, dict), "invalid source TCP header")
    return (inbound["protocol"] == "vless" and
            stream.get("network", "tcp") in ("tcp", "raw", "kcp") and
            stream.get("security", "none") in ("tls", "reality") and
            header.get("type") != "http")


def inbound_map(core, mapping, inventory, excluded):
    require(isinstance(core, dict) and isinstance(core.get("inbounds"), list),
            "source core must contain inbounds")
    source = {}
    for item in core["inbounds"]:
        require(isinstance(item, dict), "invalid source inbound")
        if item.get("protocol") not in ("vless", "hysteria") or item.get("tag") in excluded:
            continue
        tag = item.get("tag")
        require(isinstance(tag, str) and tag and tag not in source, "invalid/duplicate source tag")
        flow = item.get("x-marzban-flow") or ""
        require(flow in ("", VISION), "unsupported source inbound flow")
        require(not flow or "port" in item,
                "source flow on a fallback listener requires explicit fallback migration")
        source[tag] = {"protocol": item["protocol"],
                       "flow": flow if supports_vision(item) else ""}
    require(isinstance(mapping, dict), "inbound map must be an object")
    if isinstance(inventory, dict):
        require(inventory.get("success") is True, "target inventory API reported failure")
        inventory = inventory.get("obj")
    require(isinstance(inventory, list), "target inbound inventory must be a list")
    targets = {}
    for item in inventory:
        require(isinstance(item, dict), "invalid target inbound")
        ident = integer(item.get("id"), "target inbound ID", minimum=1)
        require(ident not in targets, "duplicate target inbound ID")
        # Require an explicit disableFlow, even false: old snapshots must be refreshed.
        require(type(item.get("disableFlow")) is bool, "target inventory lacks boolean disableFlow")
        targets[ident] = item
    assigned = set()
    for tag, ids in mapping.items():
        require(tag in source, "inbound map references an unknown/excluded source tag")
        require(isinstance(ids, list) and ids, "each source tag must map to a nonempty ID list")
        for ident in ids:
            integer(ident, "mapped inbound ID", minimum=1)
            require(ident in targets and ident not in assigned,
                    "unknown or multiply assigned target inbound ID")
            assigned.add(ident)
            target = targets[ident]
            require(target.get("protocol") == source[tag]["protocol"], "mapped protocol mismatch")
            if source[tag]["flow"]:
                stream = target.get("streamSettings")
                if isinstance(stream, str):
                    stream = decode(stream, "target streamSettings")
                require(isinstance(stream, dict) and
                        supports_vision({"protocol": "vless", "streamSettings": stream}) and
                        not target["disableFlow"], "target cannot preserve source Vision flow")
    return source, targets


def restricted_users(db, tables):
    portal = {"portal_accounts", "portal_subscriptions", "subscription_plans"}
    require(not (tables & portal) or portal <= tables, "incomplete portal schema")
    restricted = {}
    if portal <= tables:
        for row in db.execute("""SELECT a.user_id, p.local_node_selections
            FROM portal_accounts a JOIN portal_subscriptions s ON s.account_id=a.id
            JOIN subscription_plans p ON p.id=s.plan_id WHERE a.user_id IS NOT NULL"""):
            if row[1] is not None:
                selections = decode(row[1], "plan local_node_selections")
                require(selections is None or isinstance(selections, dict),
                        "invalid plan local_node_selections")
                if selections:
                    require(all(isinstance(tag, str) and (routes is None or
                            isinstance(routes, list) and all(isinstance(key, str) and re.fullmatch(r"[a-f0-9]{64}", key) for key in routes))
                            for tag, routes in selections.items()), "invalid plan route selections")
                    restricted[row[0]] = selections
    return restricted


def route_bindings(route_map, hosts, mapping):
    """Explicit source route SHA256 -> native host rows, with no merged grants."""
    require(isinstance(route_map, dict), "route map must be an object")
    if isinstance(hosts, dict):
        require(hosts.get("success") is True, "target host inventory API reported failure")
        hosts = hosts.get("obj")
    require(isinstance(hosts, list), "target host inventory must be a list")
    inventory = {}
    for host in hosts:
        require(isinstance(host, dict), "invalid target host inventory")
        ident = integer(host.get("id"), "target host ID", minimum=1)
        require(ident not in inventory, "duplicate target host ID")
        inventory[ident] = integer(host.get("inboundId"), "host inbound ID", minimum=1)
    assigned = set()
    for tag, routes in route_map.items():
        require(tag in mapping and isinstance(routes, dict), "route map references an unmapped inbound")
        for key, ids in routes.items():
            require(re.fullmatch(r"[a-f0-9]{64}", key) and isinstance(ids, list) and ids,
                    "source route map needs SHA256 route IDs and nonempty host ID lists")
            for ident in ids:
                integer(ident, "mapped host ID", minimum=1)
                require(ident in inventory and inventory[ident] in mapping[tag] and ident not in assigned,
                        "unknown, cross-inbound or multiply assigned target host ID")
                assigned.add(ident)
    return route_map


def convert(db, core, mapping, inventory, *, user_ids=(), excluded=(),
            allow_unmapped=False, traffic_policy=None, now=None, route_map=None, target_hosts=None, allow_next_plans=False):
    """Return native payloads plus private identity/provenance records."""
    tables = table_names(db)
    require({"users", "proxies", "exclude_inbounds_association", "next_plans"} <= tables,
            "unsupported Marzban schema: required tables missing")
    source, targets = inbound_map(core, mapping, inventory, set(excluded))
    restricted = restricted_users(db, tables)
    if route_map is not None:
        route_map = route_bindings(route_map, target_hosts, mapping)
    pending = {row[0] for row in db.execute("SELECT user_id FROM next_plans")}
    users = list(db.execute("SELECT * FROM users ORDER BY id"))
    selected = set(user_ids)
    require(not selected or selected <= {u["id"] for u in users}, "unknown selected user ID")
    if selected:
        users = [u for u in users if u["id"] in selected]
    require(users, "no source users selected")
    now = int(time.time()) if now is None else integer(now, "now")
    clients, identities = [], []
    seen_emails, seen_subs, seen_credentials = set(), set(), set()
    for user in users:
        uid = integer(user["id"], "legacy user ID", minimum=1)
        label = f"user ID {uid}"
        require(uid not in restricted or route_map is not None,
                f"{label}: host-specific plan grants require host-rule migration")
        require(allow_next_plans or uid not in pending, f"{label}: pending next plan is unsupported")
        status = user["status"].lower()
        require(status in ("active", "disabled", "limited", "expired"),
                f"{label}: unsupported lifecycle status (including on_hold)")
        require(user["data_limit_reset_strategy"].lower() == "no_reset",
                f"{label}: recurring quota reset requires lifecycle migration")
        require(isinstance(user["username"], str) and user["username"], f"{label}: missing username")
        email = native_identifier(f"{uid}.{user['username']}", f"{label} identity")
        sub_id = native_identifier(user["subscription_token"], f"{label} subscription token")
        used = integer(user["used_traffic"], f"{label} used traffic")
        limit = integer(user["data_limit"] if user["data_limit"] is not None else 0,
                        f"{label} traffic limit")
        expire = integer(user["expire"] if user["expire"] is not None else 0,
                         f"{label} expiry", maximum=MAX_INT64 // 1000)
        require(not used or traffic_policy == "preserve-total-as-download",
                f"{label}: nonzero historical usage needs --traffic-policy preserve-total-as-download")
        reasons = []
        if status != "active":
            reasons.append("source_status_" + status)
        if expire and expire <= now:
            reasons.append("expired_at_export")
        if limit and used >= limit:
            reasons.append("quota_exhausted_at_export")
        client = {"email": email, "subId": sub_id, "enable": not reasons,
                  "totalGB": limit, "expiryTime": expire * 1000,
                  "reset": 0, "resetDay": 0, "resetWeekday": 0, "trafficReset": "never"}
        allowed = set()
        protocols = set()
        for proxy in db.execute("SELECT id, type, settings FROM proxies WHERE user_id=? ORDER BY id", (uid,)):
            protocol = proxy["type"].lower()
            require(protocol in ("vless", "hysteria") and protocol not in protocols,
                    f"{label}: unsupported or duplicate proxy protocol")
            protocols.add(protocol)
            settings = object_json(proxy["settings"], f"{label} proxy settings")
            # Reject unknown credential-affecting fields rather than silently discard them.
            permitted = {"id", "flow"} if protocol == "vless" else {"auth"}
            require(not set(settings) - permitted, f"{label}: unsupported proxy settings fields")
            credential = settings.get("id" if protocol == "vless" else "auth")
            require(isinstance(credential, str) and credential, f"{label}: missing proxy credential")
            if protocol == "vless":
                try:
                    credential_key = str(uuid.UUID(credential))
                except ValueError:
                    raise MigrationError(f"{label}: invalid VLESS UUID") from None
                client["id"] = credential
            else:
                require(credential.strip() == credential, f"{label}: Auth whitespace is unsupported")
                client["auth"] = credential
                credential_key = credential
            require((protocol, credential_key) not in seen_credentials,
                    f"{label}: duplicate credential across users")
            seen_credentials.add((protocol, credential_key))
            excluded_tags = {r[0] for r in db.execute(
                "SELECT inbound_tag FROM exclude_inbounds_association WHERE proxy_id=?", (proxy["id"],))}
            allowed.update(tag for tag, inbound in source.items()
                           if inbound["protocol"] == protocol and tag not in excluded_tags)
        require(protocols, f"{label}: no supported proxy credentials")
        missing = sorted(allowed - set(mapping))
        require(not missing or allow_unmapped, f"{label}: allowed inbounds missing from mapping")
        mapped = allowed & set(mapping)
        flow = VISION if any(source[tag]["flow"] for tag in mapped) else ""
        for tag in mapped:
            if source[tag]["protocol"] == "vless" and flow and not source[tag]["flow"]:
                require(all(targets[ident]["disableFlow"] for ident in mapping[tag]),
                        f"{label}: no-flow target must have disableFlow=true")
        if flow:
            client["flow"] = flow
        if route_map is not None:
            granted = set()
            for tag in mapped:
                require(tag in route_map, f"{label}: missing route map for an allowed inbound")
                selected_routes = restricted.get(uid, {}).get(tag)
                if selected_routes is None:
                    selected_routes = list(route_map[tag])
                require(set(selected_routes) <= route_map[tag].keys(),
                        f"{label}: source plan selects an unmapped concrete route")
                for key in selected_routes:
                    granted.update(route_map[tag][key])
            client["clientHostRuleIds"] = sorted(granted)
            client["strictHostBindings"] = True
        require(email not in seen_emails and sub_id not in seen_subs,
                f"{label}: duplicate identity or subscription token")
        seen_emails.add(email)
        seen_subs.add(sub_id)
        clients.append({"client": client,
                        "inboundIds": sorted(ident for tag in mapped for ident in mapping[tag]),
                        "traffic": {"up": 0, "down": used, "resetCount": 0}})
        identities.append({"legacy_user_id": uid, "allocator_key": str(uid), "email": email,
                           "source_status": status, "source_used_bytes": used,
                           "disabled_reasons": reasons, "omitted_source_tags": missing})
    return clients, identities


def allocation_snapshot(path, known_ids):
    with snapshot(path) as (db, checksum):
        require({"users", "state", "owned"} <= table_names(db), "unsupported IPv6 allocator schema")
        identities, marks, addresses, orphans = set(), set(), set(), []
        for row in db.execute("SELECT identity, mark, address FROM users ORDER BY identity"):
            key, mark, address = row
            require(isinstance(key, str) and re.fullmatch(r"[1-9][0-9]*", key),
                    "allocator identity is not a legacy numeric user ID")
            integer(mark, "allocator mark", maximum=(1 << 32) - 1, minimum=1)
            try:
                parsed = ipaddress.IPv6Address(address)
            except (ValueError, TypeError):
                raise MigrationError("invalid allocator IPv6 address") from None
            require(not parsed.is_unspecified and not parsed.is_multicast and parsed.scope_id is None,
                    "unsupported allocator IPv6 address")
            address = str(parsed)
            require(key not in identities and mark not in marks and address not in addresses,
                    "duplicate allocator identity, mark, or address")
            identities.add(key)
            marks.add(mark)
            addresses.add(address)
            if int(key) not in known_ids:
                orphans.append(int(key))
        blob = Path(path).read_bytes()
        require(digest(blob) == checksum, "allocator snapshot changed during export")
    return blob, {"records": len(identities), "orphan_legacy_user_ids": sorted(orphans),
                  "sha256": checksum}


def export_bundle(args):
    core, core_hash = read_json(args.core, "source core")
    mapping, map_hash = read_json(args.inbound_map, "inbound map")
    inventory, inventory_hash = read_json(args.target_inbounds, "target inventory")
    route_map, target_hosts = None, None
    extra_sources = {}
    if args.route_map or args.target_hosts:
        require(args.route_map and args.target_hosts, "--route-map and --target-hosts must be used together")
        route_map, extra_sources["route_map_sha256"] = read_json(args.route_map, "route map")
        target_hosts, extra_sources["target_hosts_sha256"] = read_json(args.target_hosts, "target host inventory")
    with snapshot(args.database) as (db, db_hash):
        clients, identities = convert(db, core, mapping, inventory, user_ids=args.user_id,
                                      excluded=args.excluded_inbound_tag,
                                      allow_unmapped=args.allow_unmapped_inbounds,
                                      traffic_policy=args.traffic_policy, route_map=route_map, target_hosts=target_hosts)
        all_ids = {row[0] for row in db.execute("SELECT id FROM users")}
    members = {"clients.json": encode(clients), "identities.json": encode(identities),
               "import-request.json": encode({"data": json.dumps(clients, ensure_ascii=False)})}
    allocations = []
    for spec in args.ipv6_allocation:
        namespace, sep, path = spec.partition("=")
        require(sep and re.fullmatch(r"[a-zA-Z0-9_-]{1,64}", namespace),
                "IPv6 allocation must be NAME=SNAPSHOT.sqlite3")
        name = f"allocations/{namespace}.sqlite3"
        require(name not in members, "duplicate allocation namespace")
        blob, meta = allocation_snapshot(path, all_ids)
        members[name] = blob
        allocations.append({"namespace": namespace, "file": name, **meta})
    manifest = {"schema": SCHEMA, "created_at": datetime.now(timezone.utc).isoformat(),
                "client_count": len(clients), "ready_for_cutover": False,
                "partial_inbound_projection": any(i["omitted_source_tags"] for i in identities),
                "traffic_policy": args.traffic_policy or "zero-usage-only",
                "historical_traffic_direction_known": False,
                "reset_count_is_synthetic_zero": True,
                "host_bindings_projected": route_map is not None,
                "sources": {"database_sha256": db_hash, "core_sha256": core_hash,
                            "inbound_map_sha256": map_hash, "target_inbounds_sha256": inventory_hash, **extra_sources},
                "excluded_source_tags": sorted(set(args.excluded_inbound_tag)),
                "allocations": allocations,
                "not_migrated": ["portal_and_commerce", "mgma_subscription_gateway",
                                 "host_profiles_and_entitlements", "external_subscription_sources",
                                 "routing_and_dns", "egress_controller", "connection_observer",
                                 "historical_usage_reset_logs"],
                "files": {name: digest(blob) for name, blob in members.items()}}
    # Validate everything before creating a new private directory. A manifest is the final seal.
    output = Path(args.output)
    output.mkdir(mode=0o700)
    try:
        for name, blob in {**members, "manifest.json": encode(manifest)}.items():
            path = output / name
            path.parent.mkdir(mode=0o700, exist_ok=True)
            with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as file:
                file.write(blob)
    except BaseException:
        shutil.rmtree(output)
        raise
    return manifest


def verify_bundle(directory):
    root = Path(directory)
    require(root.is_dir() and not root.is_symlink(), "invalid bundle directory")
    manifest_path = root / "manifest.json"
    require(not manifest_path.is_symlink(), "bundle manifest must not be a symlink")
    manifest, _ = read_json(manifest_path, "manifest")
    require(isinstance(manifest, dict) and manifest.get("schema") == SCHEMA and
            manifest.get("ready_for_cutover") is False, "unsupported bundle manifest")
    files = manifest.get("files")
    require(isinstance(files, dict) and {"clients.json", "import-request.json", "identities.json"} <= files.keys(),
            "incomplete bundle manifest")
    for name, checksum in files.items():
        require(name in ("clients.json", "import-request.json", "identities.json") or
                re.fullmatch(r"allocations/[a-zA-Z0-9_-]{1,64}\.sqlite3", name), "invalid bundle member path")
        path = root / name
        require(not any(p.is_symlink() for p in (path, path.parent)) and path.is_file(),
                "missing or symlinked bundle member")
        require(digest(path.read_bytes()) == checksum, "bundle member checksum mismatch")
    clients, _ = read_json(root / "clients.json", "clients")
    request, _ = read_json(root / "import-request.json", "import request")
    identities, _ = read_json(root / "identities.json", "identities")
    require(isinstance(clients, list) and isinstance(identities, list) and
            len(clients) == len(identities) == manifest.get("client_count"), "bundle client count mismatch")
    require(isinstance(request, dict) and decode(request.get("data"), "import request data") == clients,
            "native import request differs from clients")
    require([c["client"]["email"] for c in clients] == [i["email"] for i in identities],
            "bundle identity mismatch")
    return manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    export = commands.add_parser("export", help="prepare a private offline import bundle")
    for flag in ("database", "core", "inbound-map", "target-inbounds", "output"):
        export.add_argument("--" + flag, required=True)
    export.add_argument("--user-id", type=int, action="append", default=[])
    export.add_argument("--excluded-inbound-tag", action="append", default=[])
    export.add_argument("--allow-unmapped-inbounds", action="store_true")
    export.add_argument("--traffic-policy", choices=["preserve-total-as-download"])
    export.add_argument("--ipv6-allocation", action="append", default=[], metavar="NAME=SNAPSHOT")
    export.add_argument("--route-map", help="source concrete route SHA256 -> native host IDs, grouped by inbound")
    export.add_argument("--target-hosts", help="native /hosts/raw inventory for grant validation")
    verify = commands.add_parser("verify", help="verify bundle checksums and request consistency")
    verify.add_argument("directory")
    args = parser.parse_args(argv)
    try:
        manifest = export_bundle(args) if args.command == "export" else verify_bundle(args.directory)
    except MigrationError as exc:
        print(f"migration error: {exc}", file=sys.stderr)
        return 2
    except (OSError, sqlite3.Error, KeyError, TypeError, AttributeError, ValueError):
        # Neither sqlite errors nor malformed JSON values should spill credentials to a terminal.
        print("migration error: invalid/unreadable input or output; check schema and paths", file=sys.stderr)
        return 2
    print(f"{args.command} OK: {manifest['client_count']} clients; ready_for_cutover=false")
    return 0


if __name__ == "__main__":
    sys.exit(main())
