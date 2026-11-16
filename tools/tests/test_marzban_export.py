"""Synthetic SQLite -> shared native-import fixture; no production data."""

from contextlib import closing, redirect_stderr, redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("marzban_export", ROOT / "tools/marzban_export.py")
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class ExportTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.db_path = self.root / "source.sqlite3"
        self.db = sqlite3.connect(self.db_path)
        self.db.row_factory = sqlite3.Row
        self.addCleanup(self.db.close)
        self.db.executescript("""
            CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT, status TEXT,
                used_traffic INTEGER, data_limit INTEGER, expire INTEGER,
                subscription_token TEXT, data_limit_reset_strategy TEXT);
            CREATE TABLE proxies(id INTEGER PRIMARY KEY, user_id INTEGER, type TEXT, settings TEXT);
            CREATE TABLE exclude_inbounds_association(proxy_id INTEGER, inbound_tag TEXT);
            CREATE TABLE next_plans(id INTEGER PRIMARY KEY, user_id INTEGER);
            CREATE TABLE portal_accounts(id INTEGER PRIMARY KEY, user_id INTEGER);
            CREATE TABLE portal_subscriptions(id INTEGER PRIMARY KEY, account_id INTEGER, plan_id INTEGER);
            CREATE TABLE subscription_plans(id INTEGER PRIMARY KEY, local_node_selections TEXT);
        """)
        self.db.executemany("INSERT INTO users VALUES(?,?,?,?,?,?,?,?)", [
            (7, "sample_a", "active", 123456, 1000000000, 4102444800, "A" * 43, "no_reset"),
            (9, "sample_b", "disabled", 0, None, None, "B" * 43, "no_reset"),
        ])
        self.db.executemany("INSERT INTO proxies VALUES(?,?,?,?)", [
            (1, 7, "VLESS", json.dumps({"id": "11111111-1111-4111-8111-111111111111"})),
            (2, 7, "Hysteria", json.dumps({"auth": "synthetic-hy2-a"})),
            (3, 9, "VLESS", json.dumps({"id": "22222222-2222-4222-8222-222222222222"})),
            (4, 9, "Hysteria", json.dumps({"auth": "synthetic-hy2-b"})),
        ])
        self.db.execute("INSERT INTO exclude_inbounds_association VALUES(3,'TEST_XHTTP')")
        self.db.commit()
        self.core = {"inbounds": [
            {"tag": "TEST_VISION", "port": 443, "protocol": "vless", "x-marzban-flow": migration.VISION,
             "streamSettings": {"network": "tcp", "security": "reality"}},
            {"tag": "TEST_XHTTP", "protocol": "vless",
             "streamSettings": {"network": "xhttp", "security": "tls"}},
            {"tag": "TEST_HY2", "protocol": "hysteria"},
        ]}
        self.mapping = {"TEST_VISION": [101], "TEST_XHTTP": [102], "TEST_HY2": [103]}
        self.inventory = [
            {"id": 101, "protocol": "vless", "disableFlow": False,
             "streamSettings": {"network": "tcp", "security": "reality"}},
            {"id": 102, "protocol": "vless", "disableFlow": True},
            {"id": 103, "protocol": "hysteria", "disableFlow": False},
        ]

    def convert(self, **kwargs):
        return migration.convert(self.db, self.core, self.mapping, self.inventory,
                                 **{"traffic_policy": "preserve-total-as-download", "now": 1800000000, **kwargs})

    def test_shared_native_fixture_and_identity(self):
        clients, identities = self.convert()
        fixture = ROOT / "internal/web/service/testdata/marzban_stage1_clients.json"
        self.assertEqual(clients, json.loads(fixture.read_text()))
        self.assertEqual([i["allocator_key"] for i in identities], ["7", "9"])
        self.assertEqual(identities[1]["disabled_reasons"], ["source_status_disabled"])

    def test_nonzero_traffic_requires_explicit_accounting_policy(self):
        with self.assertRaisesRegex(migration.MigrationError, "traffic-policy"):
            self.convert(traffic_policy=None)

    def test_missing_mapping_fails_unless_partial_projection_requested(self):
        del self.mapping["TEST_XHTTP"]
        with self.assertRaisesRegex(migration.MigrationError, "missing from mapping"):
            self.convert()
        clients, identities = self.convert(allow_unmapped=True)
        self.assertEqual(clients[0]["inboundIds"], [101, 103])
        self.assertEqual(identities[0]["omitted_source_tags"], ["TEST_XHTTP"])

    def test_one_tag_can_map_to_multiple_service_nodes(self):
        self.mapping["TEST_HY2"].append(104)
        self.inventory.append({"id": 104, "protocol": "hysteria", "disableFlow": False})
        clients, _ = self.convert()
        self.assertEqual(clients[0]["inboundIds"], [101, 102, 103, 104])

    def test_unsupported_lifecycle_never_silently_resets(self):
        for assignment in ("status='on_hold'", "data_limit_reset_strategy='month'", "expire=-60"):
            with self.subTest(assignment=assignment):
                self.db.execute("SAVEPOINT testcase")
                self.db.execute("UPDATE users SET " + assignment + " WHERE id=7")
                with self.assertRaises(migration.MigrationError):
                    self.convert()
                self.db.execute("ROLLBACK TO testcase")
        self.db.execute("INSERT INTO next_plans VALUES(1,7)")
        with self.assertRaisesRegex(migration.MigrationError, "pending next plan"):
            self.convert()

    def test_restricted_host_grants_do_not_expand_to_whole_inbound(self):
        self.db.executescript("""
            INSERT INTO portal_accounts VALUES(1,7);
            INSERT INTO portal_subscriptions VALUES(1,1,1);
            INSERT INTO subscription_plans VALUES(1,'{"TEST_VISION": ["alias-a"]}');
        """)
        with self.assertRaisesRegex(migration.MigrationError, "host-specific"):
            self.convert()

    def test_nullable_host_selections_match_source_unrestricted_behavior(self):
        self.db.executescript("""
            INSERT INTO portal_accounts VALUES(1,7);
            INSERT INTO portal_subscriptions VALUES(1,1,1);
            INSERT INTO subscription_plans VALUES(1,'null');
        """)
        self.assertEqual(len(self.convert()[0]), 2)

    def test_expired_and_exhausted_active_users_import_disabled(self):
        self.db.execute("UPDATE users SET expire=100, used_traffic=data_limit WHERE id=7")
        clients, identities = self.convert()
        self.assertFalse(clients[0]["client"]["enable"])
        self.assertEqual(identities[0]["disabled_reasons"],
                         ["expired_at_export", "quota_exhausted_at_export"])

    def test_flow_comes_from_inbound_not_legacy_proxy_setting(self):
        self.db.execute("UPDATE proxies SET settings=? WHERE id=1", (json.dumps({
            "id": "11111111-1111-4111-8111-111111111111", "flow": ""}),))
        clients, _ = self.convert()
        self.assertEqual(clients[0]["client"]["flow"], migration.VISION)
        self.inventory[1]["disableFlow"] = False
        with self.assertRaisesRegex(migration.MigrationError, "disableFlow"):
            self.convert()

    def test_fallback_flow_needs_explicit_migration_and_kcp_http_has_no_vision(self):
        del self.core["inbounds"][0]["port"]
        with self.assertRaisesRegex(migration.MigrationError, "fallback"):
            self.convert()
        self.core["inbounds"][0]["port"] = 443
        self.core["inbounds"][0]["streamSettings"] = {
            "network": "kcp", "security": "tls", "kcpSettings": {"header": {"type": "http"}}}
        clients, _ = self.convert()
        self.assertNotIn("flow", clients[0]["client"])

    def test_semantically_duplicate_uuid_never_creates_two_users(self):
        self.db.execute("UPDATE proxies SET settings=? WHERE id=3", (json.dumps({
            "id": "11111111111141118111111111111111"}),))
        with self.assertRaisesRegex(migration.MigrationError, "duplicate credential"):
            self.convert()

    def test_bad_vision_target_protocol_or_duplicate_mapping_fails(self):
        for change in ("protocol", "stream", "duplicate"):
            with self.subTest(change=change):
                saved = json.dumps([self.inventory, self.mapping])
                if change == "protocol":
                    self.inventory[0]["protocol"] = "trojan"
                elif change == "stream":
                    self.inventory[0]["streamSettings"]["network"] = "ws"
                else:
                    self.mapping["TEST_XHTTP"] = [101]
                with self.assertRaises(migration.MigrationError):
                    self.convert()
                self.inventory, self.mapping = json.loads(saved)

    def test_selection_and_effective_exclusions(self):
        # Excluded source tags must also be absent from the reviewed map.
        del self.mapping["TEST_HY2"]
        clients, _ = self.convert(user_ids=[9], excluded=["TEST_HY2"])
        self.assertEqual(len(clients), 1)
        self.assertEqual(clients[0]["inboundIds"], [101])
        with self.assertRaisesRegex(migration.MigrationError, "unknown selected"):
            self.convert(user_ids=[999])

    def make_allocation(self):
        path = self.root / "allocation.sqlite3"
        with closing(sqlite3.connect(path)) as db, db:
            db.executescript("""
                CREATE TABLE users(identity TEXT PRIMARY KEY, mark INTEGER UNIQUE, address TEXT UNIQUE);
                CREATE TABLE state(key TEXT PRIMARY KEY, value TEXT);
                CREATE TABLE owned(address TEXT PRIMARY KEY);
                INSERT INTO users VALUES('7',1007,'2001:db8::7');
                INSERT INTO users VALUES('9',1009,'2001:db8::9');
                INSERT INTO users VALUES('11',1011,'2001:db8::11');
                INSERT INTO state VALUES('next_mark','1012');
                INSERT INTO owned VALUES('2001:db8::dead');
            """)
        return path

    def cli(self, allocation=None):
        for name, value in (("core", self.core), ("map", self.mapping), ("target", self.inventory)):
            (self.root / (name + ".json")).write_text(json.dumps(value))
        args = ["export", "--database", str(self.db_path), "--core", str(self.root / "core.json"),
                "--inbound-map", str(self.root / "map.json"), "--target-inbounds", str(self.root / "target.json"),
                "--output", str(self.root / "bundle"), "--traffic-policy", "preserve-total-as-download"]
        if allocation:
            args += ["--ipv6-allocation", "test=" + str(allocation)]
        return args

    def test_private_bundle_readonly_and_allocation_ownership(self):
        allocation = self.make_allocation()
        before = self.db_path.read_bytes()
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            self.assertEqual(migration.main(self.cli(allocation)), 0)
        bundle = self.root / "bundle"
        manifest = migration.verify_bundle(bundle)
        self.assertFalse(manifest["ready_for_cutover"])
        self.assertFalse(manifest["historical_traffic_direction_known"])
        self.assertEqual(manifest["allocations"][0]["orphan_legacy_user_ids"], [11])
        self.assertEqual((bundle / "allocations/test.sqlite3").read_bytes(), allocation.read_bytes())
        self.assertEqual(self.db_path.read_bytes(), before)
        self.assertEqual(bundle.stat().st_mode & 0o777, 0o700)
        self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in bundle.rglob("*") if p.is_file()))
        self.assertNotIn("sample_a", stdout.getvalue())
        self.assertNotIn("synthetic-hy2", stdout.getvalue())
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            self.assertEqual(migration.main(self.cli(allocation)), 2)
        migration.verify_bundle(bundle)  # Refusing overwrite did not destroy prior output.
        (bundle / "clients.json").write_text("[]")
        with self.assertRaisesRegex(migration.MigrationError, "checksum"):
            migration.verify_bundle(bundle)

    def test_allocator_invalid_identity_and_normalized_duplicate_address(self):
        allocation = self.make_allocation()
        with closing(sqlite3.connect(allocation)) as db, db:
            db.execute("UPDATE users SET identity='7.sample_a' WHERE identity='7'")
        with self.assertRaisesRegex(migration.MigrationError, "legacy numeric"):
            migration.allocation_snapshot(allocation, {7, 9})
        with closing(sqlite3.connect(allocation)) as db, db:
            db.execute("UPDATE users SET identity='7' WHERE identity='7.sample_a'")
            db.execute("UPDATE users SET address='2001:db8:0:0:0:0:0:7' WHERE identity='9'")
        with self.assertRaisesRegex(migration.MigrationError, "duplicate"):
            migration.allocation_snapshot(allocation, {7, 9})

    def test_wal_snapshot_and_credential_errors_are_safe(self):
        (self.root / "source.sqlite3-wal").write_bytes(b"not-a-standalone-snapshot")
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            self.assertEqual(migration.main(self.cli()), 2)
        self.assertIn("WAL", stderr.getvalue())
        self.assertFalse((self.root / "bundle").exists())
        (self.root / "source.sqlite3-wal").unlink()
        self.db.execute("UPDATE proxies SET settings=? WHERE id=1", ('{"id":"secret-invalid-uuid"}',))
        self.db.commit()
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            self.assertEqual(migration.main(self.cli()), 2)
        self.assertNotIn("secret-invalid-uuid", stderr.getvalue())
        self.assertFalse((self.root / "bundle").exists())

    def test_verifier_refuses_symlink_and_path_traversal(self):
        with redirect_stdout(io.StringIO()):
            self.assertEqual(migration.main(self.cli()), 0)
        bundle = self.root / "bundle"
        client_path = bundle / "clients.json"
        client_path.rename(self.root / "outside.json")
        client_path.symlink_to(self.root / "outside.json")
        with self.assertRaisesRegex(migration.MigrationError, "symlink"):
            migration.verify_bundle(bundle)
        client_path.unlink()
        (self.root / "outside.json").rename(client_path)
        manifest_path = bundle / "manifest.json"
        manifest = json.loads(manifest_path.read_text())
        manifest["files"]["../outside.json"] = "no"
        manifest_path.write_text(json.dumps(manifest))
        with self.assertRaisesRegex(migration.MigrationError, "path"):
            migration.verify_bundle(bundle)


if __name__ == "__main__":
    unittest.main()
