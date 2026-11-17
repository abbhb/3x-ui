#!/usr/bin/env python3
"""Disposable native sendThrough lab. No NAT, legacy files or production hosts.

Exercises real Go lease allocation/preparation, the actual native UI editor,
native validation/persistence, and TCP/UDP through the pinned Xray core.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import time

import runtime_lab as common


def lab(binary, test_binary, node):
    if sys.platform != "linux" or os.geteuid() != 0:
        raise RuntimeError("explicit Linux root network lab required")
    binary, test_binary, node = (str(Path(p).resolve(strict=True)) for p in (binary, test_binary, node))
    suffix = secrets.token_hex(3)
    proxy, target, left, right = (prefix + suffix for prefix in ("xnp", "xnt", "xnl", "xnr"))
    namespaces, children = [], []
    with tempfile.TemporaryDirectory(prefix="3x-native-ipv6-") as directory:
        root = Path(directory)
        root.chmod(0o700)
        log = (root / "core.log").open("w")
        try:
            for ns in (proxy, target):
                common.run(["ip", "netns", "add", ns]); namespaces.append(ns)
                common.run(["ip", "-n", ns, "link", "set", "lo", "up"])
            common.run(["ip", "link", "add", left, "type", "veth", "peer", "name", right])
            for dev, ns in ((left, proxy), (right, target)):
                common.run(["ip", "link", "set", dev, "netns", ns])
                common.run(["ip", "-n", ns, "link", "set", dev, "up"])
            for ns, dev, ipv6, ipv4 in ((proxy, left, "2001:db8:aa::1/64", "192.0.2.1/24"), (target, right, "2001:db8:aa::2/64", "192.0.2.2/24")):
                common.run(["ip", "-n", ns, "address", "add", ipv6, "dev", dev, "nodad"])
                common.run(["ip", "-n", ns, "address", "add", ipv4, "dev", dev])
            common.run(["ip", "-n", target, "-6", "route", "add", "2001:db8:bb::/64", "via", "2001:db8:aa::1", "dev", right])
            for host in ("2001:db8:aa::2", "192.0.2.2"):
                children.append(subprocess.Popen(["ip", "netns", "exec", target, sys.executable, __file__, "serve", host], stdout=log, stderr=subprocess.STDOUT))

            def fixture(validate=False):
                env = ["env", "XUI_DB_TYPE=sqlite", "XUI_NATIVE_IPV6_LAB_ROOT=" + str(root), "XUI_NATIVE_IPV6_LAB_INTERFACE=" + left]
                if validate:
                    env.append("XUI_NATIVE_IPV6_LAB_VALIDATE=1")
                result = subprocess.run(["ip", "netns", "exec", proxy, *env, test_binary, "-test.run=^TestNativeIPv6RuntimeFixture$", "-test.count=1", "-test.v", "-test.timeout=60s"], capture_output=True, text=True, timeout=90)
                if result.returncode or "--- PASS: TestNativeIPv6RuntimeFixture" not in result.stdout:
                    raise RuntimeError("native service fixture failed: " + (result.stdout + result.stderr)[-2500:])

            fixture()
            editor = common.ROOT / "frontend/src/pages/xray/outbounds/native-user-ipv6.ts"
            # Type-only imports are stripped by Node 26. This runs the real UI
            # editor without installing frontend dependencies or starting a browser.
            javascript = """import {readFileSync,writeFileSync} from 'node:fs';
const {planNativeUserIPv6}=await import(process.argv[2]);
const root=process.argv[1]; const f=JSON.parse(readFileSync(root+'/fixture.json','utf8'));
let config=f.template;
for (const inbound of ['LAB4','LAB6']) config=planNativeUserIPv6(config,inbound,f.clients)(f.leases);
writeFileSync(root+'/core.json',JSON.stringify(config));"""
            common.run([node, "--input-type=module", "-e", javascript, str(root), editor.as_uri()])
            fixture(validate=True)
            data = json.loads((root / "fixture.json").read_text())
            expected = {str(lease["clientId"]) + ".sample": lease["address"] for lease in data["leases"]}

            def get(user, dest, ipv6=False, udp=False):
                payload = {"credential": common.USERS[user], "entry": "2001:db8:aa::1" if ipv6 else "127.0.0.1", "entryPort": 11081 if ipv6 else 11080, "target": dest, "port": 18082 if udp else 18080, "udp": udp}
                return json.loads(common.run(["ip", "netns", "exec", proxy, sys.executable, __file__, "request"], json.dumps(payload)))["source"]

            def start():
                process = subprocess.Popen(["ip", "netns", "exec", proxy, binary, "run", "-config", str(root / "core.json")], stdout=log, stderr=subprocess.STDOUT)
                children.append(process)
                for _ in range(30):
                    if process.poll() is not None:
                        raise RuntimeError("native core exited: " + (root / "core.log").read_text()[-2000:])
                    if get("7.sample", "2001:db8:aa::2") == expected["7.sample"]:
                        return process
                    time.sleep(.1)
                raise RuntimeError("native core did not become ready")

            process = start()
            probes = 0
            for user in common.USERS:
                for ipv6 in (False, True):
                    for udp in (False, True):
                        for destination in ("2001:db8:aa::2", "dual.test", "192.0.2.2", "v4-only.test"):
                            want = "192.0.2.1" if destination in ("192.0.2.2", "v4-only.test") else expected[user]
                            observed = get(user, destination, ipv6, udp)
                            if observed != want:
                                raise AssertionError(f"{user} entry6={ipv6} udp={udp} target={destination}: {observed} != {want}")
                            probes += 1
            process.terminate(); process.wait(timeout=10)
            common.run(["ip", "-n", proxy, "-6", "address", "del", expected["7.sample"] + "/128", "dev", left])
            fixture(validate=True)  # Native validation restores only its owned lease.
            process = start()
            if get("9.sample", "dual.test", True, True) != expected["9.sample"]:
                raise AssertionError("restart changed a user's permanent IPv6")
            print(f"PASS: {probes} native TCP/UDP probes; fixed ingress, distinct user IPv6, IPv4 routing and lease restoration")
        except Exception:
            print((root / "core.log").read_text()[-2000:], file=sys.stderr)
            raise
        finally:
            for child in reversed(children):
                if child.poll() is None:
                    child.terminate()
                try: child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    child.kill(); child.wait(timeout=5)
            log.close()
            for ns in reversed(namespaces):
                subprocess.run(["ip", "netns", "delete", ns], capture_output=True, timeout=10, check=False)
            subprocess.run(["ip", "link", "delete", left], capture_output=True, timeout=10, check=False)


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "serve":
        common.serve(sys.argv[2])
    elif len(sys.argv) > 1 and sys.argv[1] == "request":
        common.request_from_stdin()
    else:
        parser = argparse.ArgumentParser()
        parser.add_argument("--xray", required=True)
        parser.add_argument("--service-test", required=True)
        parser.add_argument("--node", required=True)
        args = parser.parse_args()
        lab(args.xray, args.service_test, args.node)
