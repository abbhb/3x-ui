"""Real pinned core, real loopback sockets; enabled explicitly by the core CI job."""

from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import uuid


ROOT = Path(__file__).resolve().parents[2]


class EchoAddress(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(("ip=" + self.client_address[0] + "\n").encode())

    def log_message(self, *_):
        pass


class IPv6Server(ThreadingHTTPServer):
    address_family = socket.AF_INET6


def port(host):
    with socket.socket(socket.AF_INET6 if ":" in host else socket.AF_INET) as sock:
        sock.bind((host, 0))
        return sock.getsockname()[1]


def request(entry, entry_port, credential, target, target_port):
    try:
        address = ipaddress.ip_address(target)
        destination = bytes([1 if address.version == 4 else 3]) + address.packed
    except ValueError:
        destination = b"\x02" + bytes([len(target)]) + target.encode("ascii")
    header = b"\x00" + uuid.UUID(credential).bytes + b"\x00\x01" + target_port.to_bytes(2, "big") + destination
    with socket.create_connection((entry, entry_port), timeout=3) as conn:
        conn.sendall(header + b"GET / HTTP/1.0\r\nHost: test\r\n\r\n")
        result = b""
        while chunk := conn.recv(16384):
            result += chunk
    return result


@unittest.skipUnless(os.environ.get("XRAY_OBSERVER_BINARY"), "requires opt-in instrumented core artifact")
class ObserverRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="3x-ui-core-", dir="/tmp")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.servers = {}
        for host, cls in (("127.0.0.1", ThreadingHTTPServer), ("::1", IPv6Server)):
            server = cls((host, 0), EchoAddress)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
            self.servers[host] = server.server_port
        self.users = {"7.sample": "11111111-1111-4111-8111-111111111111",
                      "9.sample": "22222222-2222-4222-8222-222222222222"}
        self.entries = {host: port(host) for host in self.servers}
        self.config = {"api": {"tag": "api", "services": ["HandlerService", "RoutingService"]},
                       "log": {"loglevel": "warning"}, "routing": {"rules": []},
                       "inbounds": [{"tag": "entry-" + str(ipaddress.ip_address(host).version), "listen": host,
                                     "port": value, "protocol": "vless", "settings": {"decryption": "none", "clients": [
                                         {"id": credential, "email": user} for user, credential in self.users.items()]}}
                                    for host, value in self.entries.items()],
                       "outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {
                           "finalRules": [{"action": "allow", "network": "tcp", "ip": ["127.0.0.1/32", "::1/128"]}]}}]}
        self.binary = str(Path(os.environ["XRAY_OBSERVER_BINARY"]).resolve())
        self.path = self.root / "config.json"
        self.path.write_text(json.dumps(self.config)); self.path.chmod(0o600)
        self.settings = {"binary": self.binary, "state": str(self.root / "state"), "enabled": False,
                         "inbounds": ["entry-4", "entry-6"]}
        self.policy_path = self.root / "node.json"
        self.policy_path.write_text(json.dumps(self.settings))
        self.log_path = self.root / "stdout.log"
        self.log = self.log_path.open("w")
        self.addCleanup(self.log.close)
        self.process = None
        self.addCleanup(self.stop)

    def start(self):
        self.process = subprocess.Popen([str(ROOT / "tools/egress/launch.py"), self.binary, "-c", str(self.path)],
                                        env={**os.environ, "XUI_EGRESS_CONFIG": str(self.policy_path), "XUI_CONNECTION_OBSERVER": "1"},
                                        stdout=self.log, stderr=subprocess.STDOUT)
        for _ in range(150):
            self.assertIsNone(self.process.poll(), "disposable core failed to start")
            try:
                with socket.create_connection(("127.0.0.1", self.entries["127.0.0.1"]), timeout=.1):
                    return
            except OSError:
                time.sleep(.02)
        self.fail("disposable core did not listen")

    def stop(self):
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self.process.kill(); self.process.wait(timeout=3)

    def events(self, count):
        for _ in range(100):
            records = [json.loads(line[7:]) for line in self.log_path.read_text().splitlines() if line.startswith("MZCONN ")]
            records = [r for r in records if r.get("event") == "dial"]
            if len(records) >= count:
                return records
            time.sleep(.02)
        self.fail("successful socket observations missing")

    def test_file_launcher_and_four_real_address_family_paths(self):
        self.start()
        for entry in self.entries:
            for target in self.servers:
                self.assertIn(b"ip=", request(entry, self.entries[entry], self.users["7.sample"], target, self.servers[target]))
        records = self.events(4)
        observed = set()
        for row in records:
            source = row["source"].removeprefix("tcp:")
            observed.add((6 if source.startswith("[") else 4, 6 if row["remote"].startswith("[") else 4))
            self.assertEqual(row["user"], "7.sample")
            self.assertEqual(row["outbound"], "direct")
        self.assertEqual(observed, {(4, 4), (4, 6), (6, 4), (6, 6)})
        text = json.dumps(records)
        for credential in self.users.values():
            self.assertNotIn(credential, text)

    def test_two_concurrent_users_stay_distinct_for_the_same_target(self):
        self.start()
        def dial(user):
            return request("127.0.0.1", self.entries["127.0.0.1"], self.users[user], "::1", self.servers["::1"])
        with ThreadPoolExecutor(max_workers=2) as pool:
            self.assertTrue(all(b"ip=" in response for response in pool.map(dial, self.users)))
        self.assertEqual({r["user"] for r in self.events(2)}, set(self.users))

    def test_enabled_restart_blocks_every_user_until_reconcile(self):
        self.settings["enabled"] = True
        self.policy_path.write_text(json.dumps(self.settings))
        self.start()
        for user in self.users:
            try:
                response = request("127.0.0.1", self.entries["127.0.0.1"], self.users[user], "::1", self.servers["::1"])
            except (OSError, TimeoutError):
                response = b""
            self.assertNotIn(b"ip=", response)
        status = json.loads((self.root / "state/status.json").read_text())
        self.assertFalse(status["verified"])


if __name__ == "__main__":
    unittest.main()
