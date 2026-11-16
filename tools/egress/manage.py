#!/usr/bin/env python3
"""Linux node-local IPv6 reconciliation; compatible with Marzban allocation DBs."""

import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import sqlite3
import subprocess
import sys
import time

if __package__:
    from .launch import atomic_json
    from .policy import identity, render
else:
    from launch import atomic_json
    from policy import identity, render


def command(args, data=None):
    result = subprocess.run(args, input=data, text=True, capture_output=True,
                            timeout=25, check=False)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} operation failed ({result.returncode})")
    return result.stdout


def configuration(path):
    config = json.loads(Path(path).read_text())
    if not re.fullmatch(r"[a-z0-9_]{1,20}", config["namespace"]):
        raise ValueError("invalid namespace")
    if not re.fullmatch(r"[a-zA-Z0-9_.-]{1,15}", config["interface"]):
        raise ValueError("invalid network interface")
    prefix = ipaddress.IPv6Network(config["prefix"], strict=True)
    if prefix.prefixlen != 64 or type(config["enabled"]) is not bool:
        raise ValueError("an IPv6 /64 and explicit enabled flag are required")
    if type(config["mark_prefix"]) is not int or not 1 <= config["mark_prefix"] <= 32767:
        raise ValueError("mark_prefix must be between 1 and 32767")
    if (not isinstance(config["inbounds"], list) or not config["inbounds"] or
            any(not isinstance(tag, str) or not tag or len(tag) > 256 for tag in config["inbounds"]) or
            len(set(config["inbounds"])) != len(config["inbounds"])):
        raise ValueError("managed inbound tags must be unique and nonempty")
    for field in ("state", "binary", "allocations"):
        if not Path(config[field]).is_absolute():
            raise ValueError("runtime paths must be absolute")
    return config


class Instance:
    def __init__(self, filename, *, lab=False, probe_url=None):
        self.filename = Path(filename)
        self.config = configuration(filename)
        self.state = Path(self.config["state"])
        self.prefix = ipaddress.IPv6Network(self.config["prefix"])
        if not self.prefix.is_global and not (lab and self.prefix.subnet_of(ipaddress.IPv6Network("2001:db8::/32"))):
            raise ValueError("a public IPv6 prefix is required")
        if probe_url and not lab:
            raise ValueError("a custom probe URL is limited to the root-run test lab")
        self.probe_url = probe_url
        self.dev = self.config["interface"]
        self.table = "mz6_" + self.config["namespace"]
        allocation_path = Path(self.config["allocations"])
        if allocation_path.is_symlink():
            raise ValueError("allocation database must not be a symlink")
        self.db = sqlite3.connect(allocation_path)
        self.db.row_factory = sqlite3.Row
        os.chmod(allocation_path, 0o600)
        self.db.executescript("""CREATE TABLE IF NOT EXISTS users (
            identity TEXT PRIMARY KEY, mark INTEGER UNIQUE NOT NULL, address TEXT UNIQUE NOT NULL);
            CREATE TABLE IF NOT EXISTS state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS owned (address TEXT PRIMARY KEY);""")
        previous = self.value("mark_prefix")
        if previous is not None and previous != self.config["mark_prefix"]:
            self.db.close()
            raise ValueError("stored mark prefix differs; allocation migration is required")
        for row in self.db.execute("SELECT identity, mark, address FROM users"):
            if (not re.fullmatch(r"[1-9][0-9]*", row["identity"]) or
                    type(row["mark"]) is not int or not 0 < row["mark"] < (1 << 31) or
                    row["mark"] >> 16 != self.config["mark_prefix"]):
                self.db.close()
                raise ValueError("invalid legacy allocation ownership")
            if ipaddress.IPv6Address(row["address"]) not in self.prefix:
                self.db.close()
                raise ValueError("stored allocation is outside the configured prefix")
        self.save("mark_prefix", self.config["mark_prefix"])

    def value(self, key, default=None):
        row = self.db.execute("SELECT value FROM state WHERE key=?", (key,)).fetchone()
        return json.loads(row[0]) if row else default

    def save(self, key, value):
        with self.db:
            self.db.execute("INSERT OR REPLACE INTO state VALUES (?,?)", (key, json.dumps(value)))

    def api(self, operation, *args, data=None, reply=False):
        argv = [self.config["binary"], "api", operation, "-s", "unix://" + str(self.state / "control.sock"), *args]
        if data is not None:
            argv.append("stdin:")
        result = command(argv, json.dumps(data) if data is not None else None)
        return json.loads(result) if reply else None

    def addresses(self):
        result = json.loads(command(["ip", "-j", "-6", "address", "show", "dev", self.dev]))
        if len(result) != 1:
            raise RuntimeError("configured network interface is unavailable")
        return {row["local"]: row for row in result[0]["addr_info"]}

    def allocate(self, key, *, reset=False):
        row = self.db.execute("SELECT * FROM users WHERE identity=?", (key,)).fetchone()
        if row and not reset:
            return dict(row)
        if reset and not row:
            raise ValueError("unknown permanent user number")
        occupied = {r[0] for r in self.db.execute("SELECT mark FROM users")}
        base = self.config["mark_prefix"] << 16
        if not row and len(occupied) >= 65535:
            raise RuntimeError("instance mark range exhausted")
        mark = row["mark"] if row else base | (1 + secrets.randbelow(65535))
        while not row and mark in occupied:
            mark = base | (1 + secrets.randbelow(65535))
        existing = self.addresses()
        while True:
            address = str(self.prefix.network_address + (1 + secrets.randbelow((1 << 64) - 1)))
            if address not in existing and not self.db.execute("SELECT 1 FROM owned WHERE address=?", (address,)).fetchone():
                break
        with self.db:
            self.db.execute("INSERT OR REPLACE INTO users VALUES (?,?,?)", (key, mark, address))
            self.db.execute("INSERT INTO owned VALUES (?)", (address,))
        return {"identity": key, "mark": mark, "address": address}

    def reset(self, key, expected):
        row = self.db.execute("SELECT address FROM users WHERE identity=?", (key,)).fetchone()
        if not row or ipaddress.IPv6Address(row[0]) != ipaddress.IPv6Address(expected):
            raise ValueError("allocation changed; refresh readback before resetting")
        atomic_json(self.state / "status.json", {"enabled": self.config["enabled"], "verified": False,
                    "updatedAt": int(time.time() * 1000), "reason": "reset_pending", "users": []})
        return self.allocate(key, reset=True)

    def provision(self, rows):
        before = self.addresses()
        for row in rows:
            address = row["address"]
            if address in before and before[address]["prefixlen"] != 128:
                raise RuntimeError("IPv6 /128 conflicts with a fixed ingress address")
            if address not in before:
                command(["ip", "-6", "addr", "add", address + "/128", "dev", self.dev,
                         "preferred_lft", "0", "noprefixroute"])
        for _ in range(30):
            current = self.addresses()
            if any(current.get(r["address"], {}).get("dadfailed") for r in rows):
                raise RuntimeError("duplicate IPv6 address detected")
            if all(r["address"] in current and not current[r["address"]].get("tentative") for r in rows):
                break
            time.sleep(0.15)
        else:
            raise RuntimeError("IPv6 addresses did not become ready")
        verified = set(self.value("verified_addresses", [])) & set(current)
        pending = [r["address"] for r in rows if r["address"] not in verified or r["address"] not in before]

        def probe(address):
            urls = [self.probe_url] if self.probe_url else [
                "https://www.cloudflare.com/cdn-cgi/trace", "https://one.one.one.one/cdn-cgi/trace"]
            for url in urls:
                try:
                    trace = command(["curl", "-q", "-6", "--noproxy", "*", "--interface", address,
                                     "--fail", "--silent", "--max-time", "10", url])
                    observed = [line[3:] for line in trace.splitlines() if line.startswith("ip=")]
                    if any(ipaddress.ip_address(ip) == ipaddress.ip_address(address) for ip in observed):
                        return address
                except (RuntimeError, ValueError):
                    continue
            raise RuntimeError("IPv6 source address readback failed")

        with ThreadPoolExecutor(max_workers=4) as pool:
            verified.update(pool.map(probe, pending))
        self.save("verified_addresses", sorted(verified))

    def nat(self, rows):
        tables = json.loads(command(["nft", "-j", "list", "tables"]))["nftables"]
        exists = any(t.get("table", {}).get("family") == "ip6" and
                     t["table"]["name"] == self.table for t in tables)
        desired = sorted((r["mark"], r["address"]) for r in rows)

        def signature():
            observed = json.loads(command(["nft", "-j", "list", "table", "ip6", self.table]))
            return hashlib.sha256(json.dumps([desired, self.dev, observed], sort_keys=True).encode()).hexdigest()

        if exists and rows and signature() == self.value("nat_signature"):
            return
        table = "ip6 " + self.table
        script = ["delete table " + table] if exists else []
        if rows:
            script += ["add table " + table, f"add map {table} users {{ type mark : ipv6_addr; }}",
                       f"add chain {table} post {{ type nat hook postrouting priority srcnat - 1; policy accept; }}",
                       f"add element {table} users {{ " + ", ".join(f"{r['mark']} : {r['address']}" for r in rows) + " }",
                       f'add rule {table} post oifname "{self.dev}" snat to meta mark map @users']
        if script:
            command(["nft", "-f", "-"], "\n".join(script) + "\n")
        self.save("nat_signature", signature() if rows else None)

    def reconcile(self):
        base = json.loads((self.state / "base.json").read_text())
        epoch = (self.state / "control.sock").stat().st_ino
        users, blocked = [], []
        enabled = self.config["enabled"]
        if enabled:
            emails = set()
            for tag in self.config["inbounds"]:
                emails.update(u["email"] for u in self.api("inbounduser", "-tag=" + tag, reply=True).get("users", []) if u.get("email"))
            keys = set()
            for email in sorted(emails):
                key = identity(email)
                if key is None:
                    blocked.append(email)
                    continue
                if key in keys:
                    raise RuntimeError("multiple active accounts claim the same permanent user number")
                keys.add(key)
                users.append(dict(self.allocate(key), email=email))
        rules, outbounds = render(base, self.config["inbounds"] if enabled else [], users)
        observed = self.api("lsrules", reply=True).get("rules", [])
        pairs = lambda items, field: [(r.get("ruleTag", ""), r.get(field, "")) for r in items]
        actual = pairs(observed, "tag")
        original = pairs(base["routing"]["rules"], "outboundTag")
        if [p for p in actual if not p[0].startswith("mz6-")] != original:
            raise RuntimeError("live base routing changed; restart core before reconciliation")
        if enabled:
            self.provision(users)
            self.nat(users)
        # Refuse to publish against a different core generation or a changed desired config.
        if ((self.state / "control.sock").stat().st_ino != epoch or
                json.loads((self.state / "base.json").read_text())["pid"] != base["pid"] or
                configuration(self.filename) != self.config):
            raise RuntimeError("core or desired policy changed during reconciliation")
        existing = {o["tag"] for o in self.api("lso", reply=True).get("outbounds", []) if o.get("tag", "").startswith("mz6-")}
        desired = {o["tag"] for o in outbounds}
        for outbound in outbounds:
            if outbound["tag"] not in existing:
                self.api("ado", data={"outbounds": [outbound]})
        expected = pairs(rules, "outboundTag")
        if actual != expected:
            self.api("adrules", data={"routing": dict(base["routing"], rules=rules)})
        if pairs(self.api("lsrules", reply=True).get("rules", []), "tag") != expected:
            raise RuntimeError("routing readback failed")
        for tag in sorted(existing - desired):
            self.api("rmo", tag)
        if not enabled:
            self.nat([])
        # Allocations and old owned-address reservations remain persistent when off.
        status = {"enabled": enabled, "verified": True, "pid": base["pid"], "epoch": epoch,
                  "updatedAt": int(time.time() * 1000), "reason": "", "blockedUsers": blocked,
                  "inbounds": self.config["inbounds"],
                  "policyHash": hashlib.sha256(self.filename.read_bytes()).hexdigest(),
                  "users": [{"identity": r["identity"], "address": r["address"]} for r in users]}
        atomic_json(self.state / "status.json", status)
        self.save("enabled", enabled)
        self.save("applied", {"pid": base["pid"], "epoch": epoch, "enabled": enabled})
        return status


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("action", choices=["sync", "status", "reset"])
    parser.add_argument("user_id", nargs="?", type=int)
    parser.add_argument("--expected-address", type=ipaddress.IPv6Address)
    parser.add_argument("--lab", action="store_true", help="root-run documentation-prefix network tests only")
    parser.add_argument("--probe-url")
    args = parser.parse_args(argv)
    os.umask(0o077)
    settings = configuration(args.config)
    if args.action == "status":
        value = json.loads((Path(settings["state"]) / "status.json").read_text())
        print(json.dumps({k: value[k] for k in ("enabled", "verified", "updatedAt") if k in value}))
        return
    with (args.config.parent / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        instance = Instance(args.config, lab=args.lab, probe_url=args.probe_url)
        with closing(instance.db):
            try:
                if args.action == "reset":
                    if not args.user_id or args.user_id < 1 or args.expected_address is None:
                        raise ValueError("reset requires a permanent user number and current IPv6 address")
                    instance.reset(str(args.user_id), str(args.expected_address))
                status = instance.reconcile()
            except Exception:
                atomic_json(instance.state / "status.json", {"enabled": settings["enabled"],
                            "verified": False, "updatedAt": int(time.time() * 1000),
                            "reason": "reconcile_failed", "users": []})
                raise
    print(json.dumps({"enabled": status["enabled"], "verified": status["verified"],
                      "users": len(status["users"]), "blockedUsers": len(status["blockedUsers"])}))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("3x-ui egress reconcile failed; inspect private node state", file=sys.stderr)
        sys.exit(1)
