"""3x-ui local egress compiler. First-match routes and direct settings survive."""

from copy import deepcopy
import hashlib
import json
import re


CONTROL = "mz-egress-control"
PROTOCOLS = {"vless", "vmess", "trojan", "shadowsocks", "hysteria"}


def identity(email):
    """The imported Marzban user number survives UUID rotation and rename."""
    match = re.fullmatch(r"([1-9][0-9]*)\..+", email)
    return match[1] if match else None


def snapshot(config):
    routing = config.setdefault("routing", {"rules": []})
    if routing.get("script") or routing.get("balancers"):
        raise ValueError("routing scripts/balancers need explicit egress integration")
    rules = routing.setdefault("rules", [])
    if any(r.get("ruleTag", "").startswith("mz6-") for r in rules):
        raise ValueError("reserved mz6- rule tag")
    direct = {}
    for outbound in config.get("outbounds", []):
        tag = outbound.get("tag", "")
        if tag.startswith("mz6-"):
            raise ValueError("reserved mz6- outbound tag")
        if outbound.get("protocol") != "freedom":
            continue
        stream = outbound.get("streamSettings") or {}
        settings = outbound.get("settings") or {}
        # Never turn a tunnel, redirect, chained dialer or existing mark into a direct exit.
        if (outbound.get("sendThrough") or outbound.get("proxySettings") or
                settings.get("redirect") or stream.get("sockopt") or stream.get("security") or
                stream.get("network")):
            continue
        direct[tag] = deepcopy(outbound)
    return {"routing": deepcopy(routing), "direct": direct,
            "default": config["outbounds"][0]["tag"],
            "inbounds": {i["tag"]: i["protocol"] for i in config["inbounds"]}}


def render(base, tags, users):
    rules = base["routing"]["rules"]
    slots = {}
    for inbound in tags:
        if base["inbounds"].get(inbound) not in PROTOCOLS:
            raise ValueError("managed inbound missing or protocol cannot identify users")
        at, exit_tag = len(rules), base["default"]
        for index, rule in enumerate(rules):
            if set(rule) <= {"type", "inboundTag", "outboundTag", "ruleTag"} and (
                    not rule.get("inboundTag") or inbound in rule["inboundTag"]):
                at, exit_tag = index, rule.get("outboundTag")
                break
        if exit_tag not in base["direct"]:
            raise ValueError("managed inbound does not have an eligible direct exit")
        slots.setdefault(at, []).append((inbound, exit_tag))
    result, outbounds = [], {}
    for index in range(len(rules) + 1):
        for inbound, exit_tag in slots.get(index, []):
            for user in users:
                for network in ("tcp", "udp"):
                    suffix = hashlib.sha256(exit_tag.encode()).hexdigest()[:8]
                    tag = f"mz6-{user['mark']:x}-{suffix}-{network}"
                    direct = deepcopy(base["direct"][exit_tag])
                    direct["tag"] = tag
                    # The pinned core migrates these legacy strategies into sockopt,
                    # overriding an explicit sockopt.domainStrategy. Only the new
                    # user clone adopts this policy's IPv6-first resolver contract.
                    direct.pop("targetStrategy", None)
                    direct["settings"] = direct.get("settings") or {}
                    for legacy in ("domainStrategy", "targetStrategy"):
                        direct["settings"].pop(legacy, None)
                    # Preserve freedom finalRules (including private-IP blocks), fragmentation, etc.
                    options = {"mark": user["mark"],
                               "domainStrategy": "ForceIP" if network == "tcp" else "ForceIPv6v4"}
                    if network == "tcp":
                        options["happyEyeballs"] = {"tryDelayMs": 300, "prioritizeIPv6": True,
                                                   "interleave": 1, "maxConcurrentTry": 4}
                    direct["streamSettings"] = {"sockopt": options}
                    outbounds[tag] = direct
                    rule = {"type": "field", "inboundTag": [inbound], "user": [user["email"]],
                            "network": network, "outboundTag": tag}
                    rule["ruleTag"] = "mz6-" + hashlib.sha256(
                        json.dumps(rule, sort_keys=True).encode()).hexdigest()[:24]
                    result.append(rule)
            result.append({"type": "field", "inboundTag": [inbound],
                           "ruleTag": "mz6-pending-" + hashlib.sha256(inbound.encode()).hexdigest()[:16],
                           "outboundTag": "mz6-pending"})
        if index < len(rules):
            result.append(deepcopy(rules[index]))
    if tags:
        outbounds["mz6-pending"] = {"tag": "mz6-pending", "protocol": "blackhole"}
    return result, list(outbounds.values())
