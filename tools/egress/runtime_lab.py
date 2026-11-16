#!/usr/bin/env python3
"""Disposable Linux netns lab: fixed ingress, legacy /128 ownership and real SNAT.

Requires root plus iproute2/nftables/curl and an explicitly provided core artifact.
Never reads production configuration; owns only randomized namespaces and a temp dir.
"""

import argparse
from contextlib import closing
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import uuid


ROOT = Path(__file__).resolve().parents[2]
USERS = {"7.sample": "11111111-1111-4111-8111-111111111111", "9.sample": "22222222-2222-4222-8222-222222222222"}


def run(args, data=None, timeout=30):
    result = subprocess.run(args, input=data, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError("lab command failed: " + " ".join(args[:3]))
    return result.stdout


class Echo(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers()
        self.wfile.write(("ip=" + self.client_address[0] + "\n").encode())
    def log_message(self, *_):
        pass


def serve(host):
    cls = type("AddressServer", (ThreadingHTTPServer,), {"address_family": socket.AF_INET6 if ":" in host else socket.AF_INET})
    http = cls((host, 18080 if ":" in host else 18081), Echo)
    if ":" in host:
        def udp():
            with socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as sock:
                sock.bind((host, 18082))
                while True:
                    _, source = sock.recvfrom(2048)
                    sock.sendto(("ip=" + source[0]).encode(), source)
        threading.Thread(target=udp, daemon=True).start()
    http.serve_forever()


def read_exact(conn, size):
    data = b""
    while len(data) < size:
        chunk = conn.recv(size - len(data))
        if not chunk: raise OSError("closed")
        data += chunk
    return data


def request_from_stdin():
    args = json.load(sys.stdin)
    try:
        ip = ipaddress.ip_address(args["target"])
        destination = bytes([1 if ip.version == 4 else 3]) + ip.packed
    except ValueError:
        destination = b"\x02" + bytes([len(args["target"])]) + args["target"].encode("ascii")
    udp = args.get("udp", False)
    header = b"\x00" + uuid.UUID(args["credential"]).bytes + b"\x00" + bytes([2 if udp else 1]) + args["port"].to_bytes(2, "big") + destination
    try:
        with socket.create_connection((args["entry"], args["entryPort"]), timeout=3) as conn:
            payload = b"probe" if udp else b"GET / HTTP/1.0\r\nHost: lab\r\n\r\n"
            conn.sendall(header + (len(payload).to_bytes(2, "big") if udp else b"") + payload)
            response_header = read_exact(conn, 2)
            if response_header[1]: read_exact(conn, response_header[1])
            if udp:
                data = read_exact(conn, int.from_bytes(read_exact(conn, 2), "big"))
            else:
                data = b""
                while chunk := conn.recv(8192): data += chunk
            source = next((line[3:].strip() for line in data.decode(errors="replace").splitlines() if line.startswith("ip=")), None)
            print(json.dumps({"source": source}))
    except (OSError, TimeoutError):
        print('{"source":null}')


def lab(binary):
    if sys.platform != "linux" or os.geteuid() != 0:
        raise RuntimeError("this explicit network lab requires Linux root")
    binary = str(Path(binary).resolve(strict=True))
    suffix = secrets.token_hex(3)
    proxy, target = "xup" + suffix, "xut" + suffix
    left, right = "xp" + suffix, "xt" + suffix
    owned_namespaces, children = [], []
    link_created, log = False, None
    with tempfile.TemporaryDirectory(prefix="3x-ui-netns-") as temporary:
        root = Path(temporary)
        os.chmod(root, 0o700)
        try:
            for ns in (proxy, target):
                run(["ip", "netns", "add", ns]); owned_namespaces.append(ns)
                run(["ip", "-n", ns, "link", "set", "lo", "up"])
            run(["ip", "link", "add", left, "type", "veth", "peer", "name", right])
            link_created = True
            run(["ip", "link", "set", left, "netns", proxy])
            run(["ip", "link", "set", right, "netns", target])
            for ns, dev, addr in ((proxy, left, "2001:db8:aa::1/64"), (target, right, "2001:db8:aa::2/64")):
                run(["ip", "-n", ns, "addr", "add", addr, "dev", dev, "nodad"])
                run(["ip", "-n", ns, "link", "set", dev, "up"])
            run(["ip", "-n", target, "-6", "route", "add", "2001:db8:bb::/64", "via", "2001:db8:aa::1", "dev", right])
            for ns, host in ((target, "::"), (proxy, "127.0.0.1")):
                children.append(subprocess.Popen(["ip", "netns", "exec", ns, sys.executable, __file__, "serve", host], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
            settings = {"enabled": True, "namespace": "lab", "interface": left, "prefix": "2001:db8:bb::/64", "mark_prefix": 25903,
                        "state": str(root / "state"), "binary": binary, "allocations": str(root / "allocations.sqlite3"), "inbounds": ["LAB4", "LAB6"]}
            policy = root / "node.json"; policy.write_text(json.dumps(settings)); policy.chmod(0o600)
            with closing(sqlite3.connect(settings["allocations"])) as db, db:
                db.executescript("CREATE TABLE users(identity TEXT PRIMARY KEY,mark INTEGER UNIQUE,address TEXT UNIQUE);CREATE TABLE state(key TEXT PRIMARY KEY,value TEXT);CREATE TABLE owned(address TEXT PRIMARY KEY);")
                for key in (7, 9, 11):
                    db.execute("INSERT INTO users VALUES(?,?,?)", (str(key), (25903 << 16) | key, f"2001:db8:bb::{key}"))
                    db.execute("INSERT INTO owned VALUES(?)", (f"2001:db8:bb::{key}",))
                db.execute("INSERT INTO state VALUES('mark_prefix','25903')")
            config = {"api": {"tag": "api", "services": ["HandlerService", "RoutingService"]}, "log": {"loglevel": "warning"},
                      "routing": {"rules": []}, "dns": {"hosts": {"fallback.test": ["::1", "127.0.0.1"]}},
                      "inbounds": [{"tag": tag, "listen": host, "port": port, "protocol": "vless", "settings": {"decryption": "none", "clients": [{"email": name, "id": credential} for name, credential in USERS.items()]}}
                                   for tag, host, port in [("LAB4", "127.0.0.1", 11080), ("LAB6", "2001:db8:aa::1", 11081)]],
                      "outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {"finalRules": [{"action": "allow", "ip": ["2001:db8::/32", "::1/128", "127.0.0.1/32"]}]}}]}
            path = root / "core.json"; path.write_text(json.dumps(config)); path.chmod(0o600)
            log = (root / "core.log").open("w")
            def start():
                process = subprocess.Popen(["ip", "netns", "exec", proxy, str(ROOT / "tools/egress/launch.py"), binary, "-c", str(path)], env={**os.environ, "XUI_EGRESS_CONFIG": str(policy), "XUI_CONNECTION_OBSERVER": "1"}, stdout=log, stderr=subprocess.STDOUT)
                children.append(process)
                for _ in range(100):
                    if process.poll() is not None: raise RuntimeError("disposable core exited")
                    try:
                        run(["ip", "netns", "exec", proxy, binary, "api", "inbounduser", "-s", "unix://" + str(root / "state/control.sock"), "-tag=LAB4"], timeout=2)
                        return process
                    except (RuntimeError, subprocess.TimeoutExpired): time.sleep(.05)
                raise RuntimeError("disposable core API unavailable")
            def get(user, dest="2001:db8:aa::2", target_port=18080, ipv6_entry=False, udp=False):
                payload = {"credential": USERS[user], "entry": "2001:db8:aa::1" if ipv6_entry else "127.0.0.1", "entryPort": 11081 if ipv6_entry else 11080, "target": dest, "port": target_port, "udp": udp}
                return json.loads(run(["ip", "netns", "exec", proxy, sys.executable, __file__, "request"], json.dumps(payload)))["source"]
            def sync():
                run(["ip", "netns", "exec", proxy, sys.executable, str(ROOT / "tools/egress/manage.py"), "--config", str(policy), "sync", "--lab", "--probe-url", "http://[2001:db8:aa::2]:18080/"])
            process = start()
            assert get("7.sample") is None, "restart guard leaked an unprovisioned user"
            sync()
            for user, expected in (("7.sample", "2001:db8:bb::7"), ("9.sample", "2001:db8:bb::9")):
                assert get(user) == expected, "TCP did not use the preserved per-user SNAT"
                assert get(user, ipv6_entry=True) == expected, "fixed IPv6 ingress changed egress ownership"
                assert get(user, target_port=18082, udp=True) == expected, "UDP did not use per-user SNAT"
                assert get(user, "127.0.0.1", 18081) == "127.0.0.1", "IPv4 literal was remapped"
                assert get(user, "fallback.test", 18081) == "127.0.0.1", "IPv4 fallback failed"
            process.terminate(); process.wait(timeout=5)
            process = start()
            assert get("7.sample") is None, "old kernel state bypassed restart guard"
            sync()
            assert get("7.sample") == "2001:db8:bb::7", "restart reassigned IPv6"
            addresses = json.loads(run(["ip", "-n", proxy, "-j", "-6", "address", "show", "dev", left]))[0]["addr_info"]
            assert any(a["local"] == "2001:db8:aa::1" and a["prefixlen"] == 64 for a in addresses), "fixed ingress was changed"
            with closing(sqlite3.connect(settings["allocations"])) as db:
                assert db.execute("SELECT address FROM users WHERE identity='11'").fetchone()[0] == "2001:db8:bb::11", "orphan allocation lost"
            settings["enabled"] = False; policy.write_text(json.dumps(settings)); sync()
            assert get("7.sample") == "2001:db8:aa::1", "off did not restore shared direct egress"
            print("PASS: legacy allocations, two users, fixed IPv6 ingress, TCP/UDP SNAT, IPv4 literal/fallback, guarded restart, off")
            process.terminate(); process.wait(timeout=5); log.close()
        finally:
            for process in children:
                if process.poll() is None:
                    process.terminate()
                    try: process.wait(timeout=3)
                    except subprocess.TimeoutExpired: process.kill(); process.wait(timeout=3)
            for ns in reversed(owned_namespaces):
                subprocess.run(["ip", "netns", "del", ns], capture_output=True, check=False)
            if link_created:
                subprocess.run(["ip", "link", "del", left], capture_output=True, check=False)
            if log is not None: log.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["lab", "serve", "request"])
    parser.add_argument("value", nargs="?")
    args = parser.parse_args()
    if args.mode == "serve": serve(args.value)
    elif args.mode == "request": request_from_stdin()
    else: lab(args.value)
