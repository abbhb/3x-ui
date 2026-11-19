#!/usr/bin/env python3
"""Exercise native generated subscriptions with real cores on loopback only."""
import argparse
from copy import deepcopy
from contextlib import closing
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import select
import socket
import socketserver
import struct
import subprocess
import tempfile
import threading
import time
import unittest


BINARY = {}


def free_port():
    with socket.socket() as connection:
        connection.bind(('127.0.0.1', 0))
        return connection.getsockname()[1]


def exact(connection, size):
    value = b''
    while len(value) < size:
        part = connection.recv(size - len(value))
        if not part:
            raise EOFError()
        value += part
    return value


def relay(left, right):
    while True:
        readable, _, _ = select.select([left, right], [], [], 5)
        if not readable:
            return
        for source in readable:
            content = source.recv(65536)
            if not content:
                return
            (right if source is left else left).sendall(content)


class Downlink(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            self.request.settimeout(8)
            self.server.connections += 1
            with socket.create_connection(self.server.destination, timeout=5) as upstream:
                relay(self.request, upstream)
        except OSError:
            return


class IPv6Server(socketserver.ThreadingTCPServer):
    address_family = socket.AF_INET6
    daemon_threads = True
    allow_reuse_address = True


class Target(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.proofs += 1
        body = b'native-subscription-runtime-confirmed'
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


class DNS(socketserver.BaseRequestHandler):
    def handle(self):
        packet, connection = self.request
        end = 12
        while end < len(packet) and packet[end] != 0:
            end += packet[end] + 1
        end += 1
        if end + 4 > len(packet):
            return
        typ = int.from_bytes(packet[end:end + 2], 'big')
        question = packet[12:end + 4]
        answer = b'\xc0\x0c\x00\x01\x00\x01' + struct.pack('!IH', 60, 4) + socket.inet_aton('192.0.2.42') if typ == 1 else b''
        connection.sendto(packet[:2] + struct.pack('!HHHHH', 0x8180, 1, bool(answer), 0, 0) + question + answer, self.client_address)


class SubscriptionRuntimeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        keep=os.environ.get('XUI_TEST_RUNTIME_KEEP_DIR')
        cls.workspace = None if keep else tempfile.TemporaryDirectory(prefix='xui-subscription-runtime-')
        cls.root = Path(keep) if keep else Path(cls.workspace.name)
        if keep: cls.root.mkdir(mode=0o700)
        cls.up_port, cls.down_port = [free_port() for _ in range(2)]
        env = dict(os.environ, XUI_TEST_SUBSCRIPTION_OUTPUT=str(cls.root), XUI_TEST_XHTTP_UP_PORT=str(cls.up_port), XUI_TEST_XHTTP_DOWN_PORT=str(cls.down_port))
        result = subprocess.run(['go', 'test', './internal/sub', '-run', '^(TestPersistedSubscriptionsAndCredentials|TestStage4RuntimeFixtures|TestMihomoExternalXHTTPImportProducesValidIntegerScalars)$', '-count=1', '-v'], env=env, capture_output=True, text=True, timeout=180)
        if result.returncode or result.stdout.count('--- PASS:') != 3:
            if cls.workspace: cls.workspace.cleanup()
            raise AssertionError('Native runtime fixture generation failed:\n' + result.stdout + result.stderr)

    @classmethod
    def tearDownClass(cls):
        if cls.workspace: cls.workspace.cleanup()

    def server(self, server):
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return server

    def process(self, command, log):
        handle = log.open('wb')
        process = subprocess.Popen(command, stdout=handle, stderr=subprocess.STDOUT)
        def stop():
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill(); process.wait(timeout=5)
            handle.close()
        self.addCleanup(stop)
        return process

    def wait_tcp(self, port, process):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if process.poll() is not None:
                self.fail('Owned test core exited before listening')
            try:
                with socket.create_connection(('127.0.0.1', port), timeout=.1):
                    return
            except OSError:
                time.sleep(.05)
        self.fail('Owned test core did not open loopback listener')

    def mihomo(self, name, document):
        root = self.root / name
        root.mkdir()
        path = root / 'config.json'; path.write_text(json.dumps(document, ensure_ascii=False))
        check = subprocess.run([BINARY['mihomo'], '-t', '-d', str(root), '-f', str(path)], capture_output=True, text=True, timeout=30)
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        process = self.process([BINARY['mihomo'], '-d', str(root), '-f', str(path)], root / 'core.log')
        return process

    def fetch(self, port, target):
        with closing(http.client.HTTPConnection('127.0.0.1', port, timeout=5)) as client:
            client.request('GET', f'http://127.0.0.1:{target.server_port}/proof')
            response = client.getresponse()
            return response.status, response.read()

    def target(self):
        server = ThreadingHTTPServer(('127.0.0.1', 0), Target); server.proofs = 0
        return self.server(server)

    def client_document(self, path, proxy_type):
        doc = json.loads((self.root / path).read_text())
        name = next(p['name'] for p in doc['proxies'] if p.get('type') == proxy_type)
        port = free_port()
        doc.update({'mixed-port': port, 'allow-lan': False, 'bind-address': '127.0.0.1', 'mode': 'rule', 'proxy-groups': [], 'rules': [f'MATCH,{name}'], 'dns': {'enable': False},'log-level':'debug'})
        doc.pop('tun', None)
        return doc, port

    def test_xhttp_ipv4_upload_and_ipv6_download_reach_real_xray(self):
        target = self.target()
        down = IPv6Server(('::1', self.down_port), Downlink)
        down.destination = ('127.0.0.1', self.up_port); down.connections = 0; self.server(down)
        core = self.process([BINARY['xray'], 'run', '-config', str(self.root / 'xray.json')], self.root / 'xray.log')
        self.wait_tcp(self.up_port, core)
        doc, port = self.client_document('xhttp.json', 'vless')
        self.assertIs(doc.get('ipv6'), True)
        client = self.mihomo('xhttp-client', doc); self.wait_tcp(port, client)
        try:
            result=self.fetch(port,target)
        except (OSError, TimeoutError) as error:
            result=(0, type(error).__name__)
        time.sleep(.2)
        logs='IPv6 download relay connections: '+str(down.connections)+'\n'+(self.root/'xray.log').read_text(errors='replace')+(self.root/'xhttp-client/core.log').read_text(errors='replace')
        self.assertEqual(result, (200, b'native-subscription-runtime-confirmed'),logs)
        self.assertGreater(down.connections, 0, 'independent IPv6 download connection was ignored')

    def test_external_xhttp_import_passes_mihomo_without_deprecated_options(self):
        root = self.root / 'external-import-check'; root.mkdir()
        path = root / 'config.json'
        path.write_bytes((self.root / 'external-xhttp-import.json').read_bytes())
        check = subprocess.run([BINARY['mihomo'], '-t', '-d', str(root), '-f', str(path)], capture_output=True, text=True, timeout=30)
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.assertNotIn('global-client-fingerprint', check.stdout + check.stderr)

    def test_existing_subscription_passes_mihomo_and_fake_ip_exceptions_work(self):
        document = json.loads((self.root / 'persisted-subscription.json').read_text())
        root = self.root / 'full-check'; root.mkdir(); path = root / 'config.json'; path.write_text(json.dumps(document, ensure_ascii=False))
        check = subprocess.run([BINARY['mihomo'], '-t', '-d', str(root), '-f', str(path)], capture_output=True, text=True, timeout=30)
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        upstream = self.server(socketserver.ThreadingUDPServer(('127.0.0.1', 0), DNS))
        doc = deepcopy(document); listen = free_port()
        doc.update({'proxies': [], 'proxy-groups': [], 'rules': ['MATCH,DIRECT'], 'hosts': {'proof.override.local': '198.51.100.8'}})
        doc.pop('tun', None)
        dns = doc['dns']; dns.update({'listen': f'127.0.0.1:{listen}', 'nameserver': [f'udp://127.0.0.1:{upstream.server_address[1]}'], 'default-nameserver': ['127.0.0.1'], 'proxy-server-nameserver': ['127.0.0.1'], 'nameserver-policy': {}, 'respect-rules': False})
        process = self.mihomo('dns-client', doc); self.wait_tcp(listen, process)
        def query(name):
            wire = b''.join(bytes([len(part)]) + part.encode() for part in name.split('.')) + b'\x00'
            packet = b'\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00' + wire + b'\x00\x01\x00\x01'
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.settimeout(3); sock.sendto(packet, ('127.0.0.1', listen)); response = sock.recv(4096)
            self.assertGreater(int.from_bytes(response[6:8], 'big'), 0)
            return socket.inet_ntoa(response[-4:])
        self.assertIn(ipaddress.ip_address(query('ordinary.example.test')), ipaddress.ip_network('198.18.0.0/16'))
        self.assertEqual(query('time.apple.com'), '192.0.2.42')
        self.assertEqual(query('proof.override.local'), '198.51.100.8')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mihomo', required=True)
    parser.add_argument('--xray', required=True)
    args = parser.parse_args()
    BINARY.update(mihomo=str(Path(args.mihomo).resolve(strict=True)), xray=str(Path(args.xray).resolve(strict=True)))
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(SubscriptionRuntimeTests))
    return 0 if result.wasSuccessful() else 1


if __name__ == '__main__':
    raise SystemExit(main())
