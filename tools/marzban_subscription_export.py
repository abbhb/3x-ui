#!/usr/bin/env python3
"""Offline stage 4 subscription catalog and companion export. Never contacts a panel."""
import argparse
import base64
from copy import deepcopy
import json
import os
from pathlib import Path
import sys

from marzban_export import require, MigrationError, snapshot, read_json, encode, digest, table_names
from marzban_portal_export import rows, j, stamp, required_bool

SCHEMA = "marzban-subscriptions-to-3x-ui/v1"
LOCAL = "__MGMA_LOCAL_NODES__"


def public_stream(value, download=False):
    allowed = {"address", "port", "network", "security", "tlsSettings", "realitySettings", "xhttpSettings", "splithttpSettings"}
    out = {k: deepcopy(v) for k, v in value.items() if k in allowed}
    for key in ("tlsSettings", "realitySettings"):
        if key in out:
            names = {"serverName", "fingerprint", "alpn", "allowInsecure"}
            if key == "realitySettings":
                names |= {"publicKey", "password", "shortId"}
            out[key] = {k: v for k, v in out[key].items() if k in names}
    for key in ("xhttpSettings", "splithttpSettings"):
        if key not in out:
            continue
        transport = out[key]
        for target in (transport, transport.get("extra", {})):
            if target.get("downloadSettings"):
                require(not download, "nested XHTTP download connections are not supported")
                target["downloadSettings"] = public_stream(target["downloadSettings"], True)
    return out


def resolved_inbounds(core, tls_names=None):
    result = {}
    for raw in core["inbounds"]:
        if raw.get("protocol") not in ("vless", "hysteria"):
            continue
        require(type(raw.get("port")) is int, "stage 4 requires an explicit backend port; migrate fallbacks separately")
        stream = raw.get("streamSettings") or {}
        net = stream.get("network", "tcp")
        transport = stream.get(net + "Settings") or {}
        sec = stream.get("security") or "none"
        inbound = dict(tag=raw["tag"], protocol=raw["protocol"], port=raw["port"], network=net,
                       tls=sec, sni=[], host=[], path="", header_type="", flow_override=raw.get("x-marzban-flow"))
        if sec == "reality":
            tls = stream.get("realitySettings") or {}
            key = tls.get("publicKey")
            if not key:
                try:
                    from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey
                    from cryptography.hazmat.primitives import serialization
                    key = base64.urlsafe_b64encode(X25519PrivateKey.from_private_bytes(base64.urlsafe_b64decode(tls["privateKey"] + "=" * (-len(tls["privateKey"]) % 4))).public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)).decode().rstrip("=")
                except (ImportError, KeyError, ValueError):
                    raise MigrationError("REALITY requires its public key or cryptography for offline derivation") from None
            inbound.update(fp="chrome", sni=tls.get("serverNames", []), pbk=key, sids=tls.get("shortIds"))
        elif sec == "tls":
            names = (tls_names or {}).get(raw["tag"])
            certificates = (stream.get("tlsSettings") or {}).get("certificates", [])
            if names is None and certificates:
                names = []
                try:
                    from cryptography import x509
                    for cert in certificates:
                        content = cert.get("certificate")
                        if isinstance(content, list):
                            content = "\n".join(content)
                        blob = content.encode() if content else Path(cert["certificateFile"]).read_bytes()
                        parsed = x509.load_pem_x509_certificate(blob)
                        try:
                            names += parsed.extensions.get_extension_for_class(x509.SubjectAlternativeName).value.get_values_for_type(x509.DNSName)
                        except x509.ExtensionNotFound:
                            pass
                except (ImportError, OSError, KeyError, ValueError):
                    raise MigrationError("TLS names require a frozen certificate or explicit --tls-name-map") from None
            inbound["sni"] = names or []
        if net in ("xhttp", "splithttp"):
            inbound.update(path=transport.get("path", ""), host=[transport.get("host", "")], xhttpSettings=deepcopy(transport), xhttpProfiles=deepcopy(raw.get("x-marzban-xhttp-profiles", [])))
        elif net in ("tcp", "raw"):
            header = transport.get("header") or {}
            req = header.get("request") or {}
            paths = req.get("path") or []
            inbound.update(header_type=header.get("type", ""), path=paths[0] if paths else "", host=(req.get("headers") or {}).get("Host") or [])
        else:
            require(net in ("hysteria", "quic", "ws", "grpc", "httpupgrade"), "unsupported source transport for route identity")
            inbound["path"] = transport.get("serviceName", "") if net == "grpc" else transport.get("path", "")
            host = transport.get("authority", "") if net == "grpc" else transport.get("host", "") or (transport.get("headers") or {}).get("Host", "")
            inbound["host"] = [host] if host else []
        result[raw["tag"]] = inbound
    return result


def cached_host(row):
    def split(value):
        return [x.strip() for x in value.split(",")] if value else []
    return dict(remark=row["remark"], id=row["id"], cascade_mode=row.get("cascade_mode") or "off", address=split(row["address"]),
                port=row.get("port"), path=row.get("path") or None, sni=split(row.get("sni")), host=split(row.get("host")),
                alpn="" if row.get("alpn") in (None, "none") else row["alpn"],
                fingerprint="" if row.get("fingerprint") in (None, "none") else row["fingerprint"],
                tls=None if row.get("security") in (None, "inbound_default") else row["security"],
                allowinsecure=row.get("allowinsecure"), mux_enable=bool(row.get("mux_enable", 0)), fragment_setting=row.get("fragment_setting"),
                noise_setting=row.get("noise_setting"), random_user_agent=bool(row.get("random_user_agent", 0)), use_sni_as_host=bool(row.get("use_sni_as_host", 0)))


def route_key(host, profile, inbound):
    endpoint = {k: v for k, v in host.items() if k not in {"remark", "id", "cascade_mode"}}
    transport = {k: v for k, v in (profile or {}).items() if k not in {"label", "enabled"}}
    defaults = {k: inbound.get(k) for k in ("network", "tls", "port", "path", "sni", "host", "fp", "alpn", "pbk", "sids", "header_type", "xhttpSettings", "flow_override")}
    # The source intentionally uses ensure_ascii=True for concrete local route identities.
    return digest(json.dumps([endpoint, transport, defaults], sort_keys=True, separators=(",", ":")).encode())


def source_routes(db, core, mapping, tls_names=None):
    inbounds = resolved_inbounds(core, tls_names)
    tables = table_names(db)
    require("hosts" in tables, "stage 4 requires frozen source hosts")
    result = {tag: {} for tag in mapping}
    for row in rows(db, "hosts", tables):
        tag = row["inbound_tag"]
        if tag not in mapping or row.get("is_disabled"):
            continue
        inbound = inbounds[tag]
        host = cached_host(row)
        profiles = (inbound.get("xhttpProfiles") if inbound["network"] in ("xhttp", "splithttp") else None) or [None]
        require(len(profiles) <= 8, "too many source XHTTP profiles")
        for profile in profiles:
            key = route_key(host, profile, inbound)
            name = host["remark"] + (" · " + profile["label"] if profile else "")
            stream = ""
            if profile and profile.get("streamSettings") is not None:
                settings = public_stream(profile["streamSettings"])
                require(settings == profile["streamSettings"], "XHTTP profile contains server-only or unknown connection fields")
                settings.update(address=profile["address"], port=profile["port"])
                stream = json.dumps(settings, ensure_ascii=False)
            for target in mapping[tag]:
                for address in host["address"] or [""]:
                    native = dict(inboundId=target, groupId="marzban_route_" + key, sortOrder=len(result[tag]), remark=name, address=address,
                                  port=host["port"] or inbound["port"], security=host["tls"] or "same", sni=next(iter(host["sni"]), ""), snis=host["sni"],
                                  hostHeader=next(iter(host["host"]), ""), path=host["path"] or "", alpn=host["alpn"].split(",") if host["alpn"] else [],
                                  fingerprint=host["fingerprint"], allowInsecure=bool(host["allowinsecure"]), randomCount=1,
                                  useSniAsHost=bool(host["use_sni_as_host"]), isDisabled=not (profile or {}).get("enabled", True))
                    route = dict(mihomo_only=profile is not None, cascade_mode=row.get("cascade_mode") or "off", entry_node_id=row.get("cascade_entry_id"),
                                 entry_ciphertext=row.get("cascade_entry_ciphertext") or "", client_stream=stream)
                    result[tag].setdefault(key, []).append((native, route))
    require(all(result.values()), "every mapped inbound must have a source host")
    return result


def catalog(db, core, mapping, tls_names=None, first_id=10001):
    routes, hosts = {}, []
    for tag, options in source_routes(db, core, mapping, tls_names).items():
        routes[tag] = {}
        for key, variants in options.items():
            routes[tag][key] = []
            for host, _ in variants:
                host["id"] = first_id; first_id += 1
                hosts.append(host); routes[tag][key].append(host["id"])
    return routes, hosts


def load_policy(template_path):
    path = Path(template_path)
    blob = path.read_bytes()
    try:
        document = json.loads(blob)
    except (ValueError, UnicodeError):
        try:
            import yaml
            from jinja2.sandbox import SandboxedEnvironment
            environment = SandboxedEnvironment(autoescape=False)
            environment.filters["yaml"] = lambda v: yaml.safe_dump(v, allow_unicode=True, sort_keys=False)
            environment.filters["except"] = lambda v, *keys: {k: item for k, item in v.items() if k not in keys}
            rendered = environment.from_string(blob.decode()).render(conf={"proxies": [], "proxy-groups": []}, proxy_remarks=[LOCAL])
            document = yaml.safe_load(rendered)
        except Exception:
            # Template failures must not print context that can contain secrets.
            raise MigrationError("policy template requires valid JSON or PyYAML and Jinja2") from None
    require(isinstance(document, dict), "invalid policy template")
    document.pop("proxies", None)
    secure = bool(document.pop("x-mgma-secure-dns", False))
    flat = bool(document.pop("x-mgma-flat-primary", False))
    groups = document.get("proxy-groups", [])
    primary = "🚀 节点选择" if any(g.get("name") == "🚀 节点选择" for g in groups) else "PROXY"
    if flat:
        for group in groups:
            if group.get("name") == primary:
                group["proxies"] = [LOCAL] + group.get("proxies", [])
                group.pop("include-all-proxies", None)
    return document, primary, secure, digest(blob)


def extend(db, bundle, core, mapping, route_map, target_hosts, template_path, tls_names=None):
    tables = table_names(db)
    prepared = source_routes(db, core, mapping, tls_names)
    hosts_by_id = {h["id"]: h for h in target_hosts}
    require(all(set(prepared[tag]) == set(route_map[tag]) for tag in mapping), "route map must cover exactly the frozen source catalog")
    hosts, extensions = [], []
    used = set()
    for tag, options in prepared.items():
        for key, variants in options.items():
            ids = route_map[tag][key]
            require(len(ids) == len(variants), "route map loses or expands a concrete host variant")
            for ident, (expected, route) in zip(ids, variants):
                require(ident not in used and ident in hosts_by_id, "duplicate/missing concrete host projection")
                target = hosts_by_id[ident]
                require(all(target.get(k) == v for k, v in expected.items()), "target host parameters differ from the frozen source catalog")
                used.add(ident); hosts.append(target)
                extensions.append(dict(host_id=ident, revision=1, **route))
    sources, nodes = [], []
    for row in rows(db, "external_subscription_sources", tables):
        owner = row.get("owner_account_id")
        require(owner is None or any(a["id"] == owner for a in bundle["accounts"]), "external source owner is missing")
        sources.append(dict(id=row["id"], owner_account_id=owner, scope_key=owner or 0, name=row["name"], group_name=row["group_name"],
                            url_ciphertext=row.get("url_ciphertext") or "", enabled=required_bool(row["enabled"]),
                            refresh_interval_minutes=row["refresh_interval_minutes"], url_revision=row.get("url_revision", 0),
                            next_refresh_at=stamp(row.get("next_refresh_at")), status=row["status"], last_error_code=row.get("last_error_code") or "",
                            last_attempt_at=stamp(row.get("last_attempt_at")), last_success_at=stamp(row.get("last_success_at")),
                            node_count=row["node_count"], skipped_node_count=row["skipped_node_count"],
                            created_at=stamp(row["created_at"]), updated_at=stamp(row["updated_at"])))
    for row in rows(db, "external_subscription_nodes", tables):
        nodes.append(dict(id=row["id"], source_id=row["source_id"], external_key=row["external_key"], name=row["name"], proxy_type=row["proxy_type"],
                          payload_ciphertext=row["payload_ciphertext"], position=row["position"], is_active=required_bool(row["is_active"])))
    selected = [dict(r) for r in db.execute("SELECT selection_id,node_id FROM plan_external_selected_nodes ORDER BY selection_id,node_id")] if "plan_external_selected_nodes" in tables else []
    selections = rows(db, "plan_external_source_selections", tables)
    source_plans = {r["id"]: r for r in rows(db, "subscription_plans", tables)}
    for plan in bundle["plans"]:
        plan["external_selections"] = [dict(source_id=r["source_id"], mode=r["mode"], node_ids=[n["node_id"] for n in selected if n["selection_id"] == r["id"]]) for r in selections if r["plan_id"] == plan["id"]]
        members = j(source_plans[plan["id"]].get("group_members"), {})
        translated = {}
        for group, refs in members.items():
            out = []
            for ref in refs:
                if ref == "external":
                    out.append(ref)
                else:
                    require(ref.startswith("inbound:") and ref[8:] in mapping, "unmapped policy group inbound")
                    out += ["inbound:" + str(ident) for ident in mapping[ref[8:]]]
            translated[group] = list(dict.fromkeys(out))
        local = j(source_plans[plan["id"]].get("local_node_selections"), {})
        tags = j(source_plans[plan["id"]]["inbound_tags"], [])
        auto = sorted({ident for tag in tags if local.get(tag) is None for ident in mapping[tag]})
        plan["auto_host_inbound_ids"] = auto
        plan["host_ids"] = [ident for ident in plan["host_ids"] if hosts_by_id[ident]["inboundId"] not in auto]
        plan.update(group_members=translated, compatibility_pending=False, is_visible=required_bool(source_plans[plan["id"]]["is_visible"]))
    for subscription in bundle["subscriptions"]:
        subscription["host_ids"] = next(p["host_ids"] for p in bundle["plans"] if p["id"] == subscription["plan_id"])
    document, primary, secure, template_hash = load_policy(template_path)
    rules = rows(db, "subscription_rules", tables)
    if rules and rules[0].get("rules") is not None:
        document["rules"] = j(rules[0]["rules"], [])
    order = rows(db, "subscription_group_order", tables)
    group_order = j(order[0].get("group_names"), []) if order else []
    secret = os.environ.get("EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "")
    has_cipher = any(s["url_ciphertext"] for s in sources) or bool(nodes) or any(r["entry_ciphertext"] for r in extensions)
    require(not has_cipher or len(secret.encode()) >= 32, "external ciphertext import requires the source encryption key for its fingerprint")
    extension = dict(schema=SCHEMA, external_key_sha256=digest(secret.encode()) if has_cipher else "", sources=sources, nodes=nodes, hosts=hosts,
                     routes=extensions, policy=dict(revision=1, enabled=True, primary_group=primary, secure_dns=secure,
                                                   document=json.dumps(document, ensure_ascii=False), group_order=group_order))
    bundle["subscriptions_extension"] = extension
    return template_hash


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True)
    parser.add_argument("--core", required=True)
    parser.add_argument("--inbound-map", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--tls-name-map")
    parser.add_argument("--first-host-id", type=int, default=10001)
    args = parser.parse_args(argv)
    try:
        core, _ = read_json(args.core, "core")
        mapping, _ = read_json(args.inbound_map, "inbound map")
        names = read_json(args.tls_name_map, "TLS names")[0] if args.tls_name_map else None
        require(args.first_host_id > 0, "first host ID must be positive")
        with snapshot(args.database) as (db, _):
            routes, hosts = catalog(db, core, mapping, names, args.first_host_id)
        root = Path(args.output);root.mkdir(mode=0o700)
        for name, value in (("route-map.json", routes), ("target-hosts.json", hosts)):
            with os.fdopen(os.open(root / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as handle:
                handle.write(encode(value))
        print(f"Private concrete route catalog prepared: {len(hosts)} hosts")
        return 0
    except (MigrationError, OSError, ValueError, KeyError, TypeError):
        print("Stage 4 catalog rejected: check frozen core, host metadata and TLS public parameters", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
