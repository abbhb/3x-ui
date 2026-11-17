"""Synthetic source commerce fixture shared with the actual Go offline importer."""
import hashlib
import hmac
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import marzban_portal_export as portal

PEPPER = "synthetic-mgma-pepper-at-least-thirty-two-bytes"
LEGACY_KEY = "synthetic-legacy-key-at-least-thirty-two-bytes"
PASSWORD_HASH = "$2b$10$abcdefghijklmnopqrstuu0sWZmk17w8plDibgv.s.Cyf0pXRxXW."  # synthetic-password
STAMP = "2026-11-01 00:00:00.000000"


def fixture(db):
    db.row_factory = sqlite3.Row
    schemas = {
        "users": "id INTEGER PRIMARY KEY, username TEXT, status TEXT, used_traffic INTEGER, data_limit INTEGER, expire INTEGER, subscription_token TEXT, data_limit_reset_strategy TEXT, created_at TEXT, sub_revoked_at TEXT, sub_access_token_digest TEXT, sub_access_issued_at TEXT, sub_access_expires_at TEXT, sub_access_consumed_at TEXT",
        "proxies": "id INTEGER PRIMARY KEY, user_id INTEGER, type TEXT, settings TEXT",
        "exclude_inbounds_association": "proxy_id INTEGER, inbound_tag TEXT",
        "next_plans": "id INTEGER PRIMARY KEY, user_id INTEGER, data_limit INTEGER, expire INTEGER, add_remaining_traffic INTEGER, fire_on_either INTEGER",
        "portal_accounts": "id INTEGER PRIMARY KEY, username TEXT, hashed_password TEXT, wallet_balance_minor INTEGER, is_active INTEGER, user_id INTEGER, created_at TEXT, updated_at TEXT",
        "subscription_plans": "id INTEGER PRIMARY KEY, name TEXT, description TEXT, price_minor INTEGER, currency TEXT, duration_days INTEGER, data_limit INTEGER, inbound_tags TEXT, group_members TEXT, local_node_selections TEXT, is_visible INTEGER, created_at TEXT, updated_at TEXT",
        "portal_subscriptions": "id INTEGER PRIMARY KEY, account_id INTEGER, plan_id INTEGER, plan_name TEXT, price_paid_minor INTEGER, currency TEXT, duration_days INTEGER, data_limit INTEGER, inbound_tags TEXT, starts_at TEXT, expires_at TEXT, purchased_at TEXT, disabled_at TEXT",
        "portal_purchases": "id INTEGER PRIMARY KEY, account_id INTEGER, plan_id INTEGER, kind TEXT, idempotency_key TEXT, actor_admin TEXT, plan_name TEXT, amount_minor INTEGER, currency TEXT, duration_days INTEGER, data_limit INTEGER, inbound_tags TEXT, balance_before_minor INTEGER, balance_after_minor INTEGER, effective_expires_at TEXT, created_at TEXT",
        "wallet_transactions": "id INTEGER PRIMARY KEY, account_id INTEGER, kind TEXT, idempotency_key TEXT, actor_admin TEXT, purchase_id INTEGER, note TEXT, amount_minor INTEGER, balance_after_minor INTEGER, created_at TEXT",
        "portal_invitation_codes": "id INTEGER PRIMARY KEY, code_digest TEXT, code_prefix TEXT, note TEXT, valid_from TEXT, expires_at TEXT, max_uses INTEGER, use_count INTEGER, is_active INTEGER, created_by TEXT, created_at TEXT, updated_at TEXT",
        "portal_invitation_uses": "id INTEGER PRIMARY KEY, invitation_id INTEGER, account_id INTEGER, source_ip TEXT, used_at TEXT",
        "portal_ip_blocks": "id INTEGER PRIMARY KEY, network TEXT, reason TEXT, source TEXT, is_active INTEGER, expires_at TEXT, created_by TEXT, created_at TEXT, updated_at TEXT, revoked_at TEXT",
        "portal_security_attempts": "id INTEGER PRIMARY KEY, source_ip TEXT, kind TEXT, failure_count INTEGER, window_started_at TEXT, last_failed_at TEXT",
        "portal_security_settings": "id INTEGER PRIMARY KEY, auto_block_enabled INTEGER, login_failure_limit INTEGER, registration_failure_limit INTEGER, login_window_seconds INTEGER, registration_window_seconds INTEGER, auto_block_seconds INTEGER",
        "mgma_settings": "id INTEGER PRIMARY KEY, mode TEXT, ttl_seconds INTEGER, single_use INTEGER, source_mode TEXT, custom_cidrs TEXT",
        "jwt": "id INTEGER PRIMARY KEY, secret_key TEXT",
        "user_usage_logs": "id INTEGER PRIMARY KEY, user_id INTEGER, used_traffic_at_reset INTEGER, reset_at TEXT",
        "plan_external_source_selections": "id INTEGER PRIMARY KEY, plan_id INTEGER, source_id INTEGER, mode TEXT",
    }
    for table, definition in schemas.items():
        db.execute(f'CREATE TABLE "{table}" ({definition})')
    def insert(table, *values):
        db.execute(f'INSERT INTO "{table}" VALUES ({",".join("?" for _ in values)})', values)
    insert("users", 7, "sample_a", "active", 123456, 1000000000, 4102444800, "A" * 43, "no_reset", STAMP, None,
           hmac.new(PEPPER.encode(), ("G" * 43).encode(), hashlib.sha256).hexdigest(), STAMP, None, None)
    insert("users", 9, "sample_b", "disabled", 0, None, None, "B" * 43, "no_reset", STAMP, None, None, None, None, None)
    insert("proxies", 1, 7, "VLESS", json.dumps({"id": "11111111-1111-4111-8111-111111111111"}))
    insert("proxies", 2, 7, "Hysteria", json.dumps({"auth": "synthetic-hy2-a"}))
    insert("proxies", 3, 9, "VLESS", json.dumps({"id": "22222222-2222-4222-8222-222222222222"}))
    insert("proxies", 4, 9, "Hysteria", json.dumps({"auth": "synthetic-hy2-b"}))
    insert("portal_accounts", 5, "Sample_A", PASSWORD_HASH, 1701, 1, 7, STAMP, STAMP)
    insert("portal_accounts", 6, "unactivated", PASSWORD_HASH, 0, 0, None, STAMP, STAMP)
    tags = json.dumps(["TEST_VISION", "TEST_XHTTP", "TEST_HY2"])
    insert("subscription_plans", 4, "Synthetic plan", "Synthetic only", 1299, "CNY", 30, 1000000000, tags, "{}", "{}", 1, STAMP, STAMP)
    insert("portal_subscriptions", 1, 5, 4, "Synthetic plan", 1299, "CNY", 30, 1000000000, tags, STAMP, "2100-01-01 00:00:00", STAMP, None)
    insert("portal_purchases", 10, 5, 4, "self_purchase", "source-purchase", None, "Synthetic plan", 1299, "CNY", 30, 1000000000, tags, 3000, 1701, "2100-01-01 00:00:00", STAMP)
    insert("wallet_transactions", 1, 5, "admin_credit", "source-credit", "synthetic-admin", None, "synthetic credit", 3000, 3000, STAMP)
    insert("wallet_transactions", 2, 5, "purchase_debit", "source-purchase", None, 10, "Purchase: Synthetic plan", -1299, 1701, STAMP)
    insert("portal_invitation_codes", 2, hashlib.sha256(("I" * 43).encode()).hexdigest(), "IIIIIIII", "synthetic", None, None, 2, 1, 1, "synthetic-admin", STAMP, STAMP)
    insert("portal_invitation_uses", 1, 2, 5, "192.0.2.1", STAMP)
    insert("portal_ip_blocks", 1, "203.0.113.8/32", "synthetic", "manual", 1, None, "synthetic-admin", STAMP, STAMP, None)
    insert("portal_security_attempts", 1, "192.0.2.3", "portal_login", 2, STAMP, STAMP)
    insert("portal_security_settings", 1, 1, 8, 5, 900, 600, 86400)
    insert("mgma_settings", 1, "dual", 0, 0, "any", "[]")
    insert("jwt", 1, LEGACY_KEY)
    insert("user_usage_logs", 1, 7, 97, STAMP)
    insert("next_plans", 1, 9, 100, None, 1, 1)
    db.commit()
    core = {"inbounds": [
        {"tag": "TEST_VISION", "port": 443, "protocol": "vless", "x-marzban-flow": "xtls-rprx-vision", "streamSettings": {"network": "tcp", "security": "reality"}},
        {"tag": "TEST_XHTTP", "protocol": "vless", "streamSettings": {"network": "xhttp", "security": "tls"}},
        {"tag": "TEST_HY2", "protocol": "hysteria"}]}
    mapping = {"TEST_VISION": [101], "TEST_XHTTP": [102], "TEST_HY2": [103]}
    inventory = [{"id": 101, "protocol": "vless", "disableFlow": False, "streamSettings": {"network": "tcp", "security": "reality"}}, {"id": 102, "protocol": "vless", "disableFlow": True}, {"id": 103, "protocol": "hysteria", "disableFlow": False}]
    routes = {tag: {str(index) * 64: [1000 + index]} for index, tag in enumerate(mapping, 1)}
    hosts = [{"id": 1000 + index, "inboundId": ident[0]} for index, ident in enumerate(mapping.values(), 1)]
    return core, mapping, inventory, routes, hosts


class PortalExportTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.addCleanup(self.db.close)
        self.inputs = fixture(self.db)
        self.env = patch.dict(os.environ, {"MGMA_TOKEN_PEPPER": PEPPER})
        self.env.start()
        self.addCleanup(self.env.stop)

    def build(self):
        return portal.build(self.db, *self.inputs, now=1800000000)

    def test_shared_import_fixture_and_private_data(self):
        bundle, _, _ = self.build()
        fixture_path = ROOT / "internal/web/service/testdata/marzban_stage3_portal.json"
        self.assertEqual(bundle, json.loads(fixture_path.read_text()))
        self.assertEqual(bundle["accounts"][0]["username"], "sample_a")
        self.assertEqual(bundle["accounts"][0]["password_hash"], PASSWORD_HASH)
        self.assertEqual(bundle["accounts"][0]["wallet_balance_minor"], 1701)
        self.assertTrue(bundle["clients"][0]["client"]["requireMGMA"])
        self.assertTrue(bundle["next_plans"][0]["paused"])
        self.assertEqual(bundle["usage_history"][0]["used_traffic"], 97)
        self.assertNotIn(LEGACY_KEY, json.dumps(bundle))
        self.assertNotIn(PEPPER, json.dumps(bundle))
        self.assertNotIn('"' + "G" * 43 + '"', json.dumps(bundle))

    def test_explicit_archive_retains_disabled_unmanaged_identity_and_raw_credentials(self):
        original = dict(self.db.execute("SELECT * FROM users WHERE id=7").fetchone())
        original.update(id=11, username="archived_disabled", status="disabled", subscription_token="archive-11", data_limit_reset_strategy="day")
        self.db.execute("INSERT INTO users (" + ",".join(original) + ") VALUES (" + ",".join("?" for _ in original) + ")", tuple(original.values()))
        self.db.execute("INSERT INTO proxies VALUES (611,11,'VLESS',?)", (json.dumps({"id":"11111111-1111-4111-8111-000000000011"}),))
        self.db.commit()
        with self.assertRaisesRegex(portal.MigrationError, "recurring quota"):
            self.build()
        bundle, identities, future = portal.build(self.db, *self.inputs, now=1800000000, archive_unmanaged_disabled=True)
        self.assertIn(11, bundle["reserved_ids"])
        self.assertEqual([i["legacy_user_id"] for i in identities], [7,9])
        archived = future["archived_legacy_users"]
        self.assertEqual(archived["users"], [original])
        self.assertEqual(archived["proxies"][0]["user_id"],11)
        self.assertIn("000000000011",archived["proxies"][0]["settings"])
        self.db.execute("UPDATE users SET status='active' WHERE id=11")
        with self.assertRaisesRegex(portal.MigrationError, "recurring quota"):
            portal.build(self.db, *self.inputs, now=1800000000, archive_unmanaged_disabled=True)

    def test_retired_historical_routes_do_not_become_current_entitlements(self):
        self.db.execute("UPDATE portal_purchases SET inbound_tags=? WHERE id=10", (json.dumps(["RETIRED_HK", "TEST_VISION"]),))
        bundle, _, future = self.build()
        purchase=next(x for x in bundle["purchases"] if x["id"]==10)
        self.assertEqual(purchase["legacy_inbound_tags"],["RETIRED_HK","TEST_VISION"])
        self.assertEqual(purchase["inbound_ids"],[101])
        self.assertEqual(purchase["host_ids"],[1001])
        self.assertEqual(bundle["subscriptions"][0]["inbound_ids"],[101,102,103])
        self.assertEqual(future["source_commerce_snapshot"]["portal_purchases"][0]["inbound_tags"],json.dumps(["RETIRED_HK","TEST_VISION"]))

    def test_clientless_historical_subscription_remains_clientless(self):
        self.db.execute("UPDATE portal_accounts SET user_id=NULL WHERE id=5")
        self.db.execute("UPDATE portal_subscriptions SET disabled_at=? WHERE account_id=5", (STAMP,))
        bundle, _, _ = self.build()
        account=next(a for a in bundle["accounts"] if a["id"]==5)
        self.assertIsNone(account["client_id"])
        sub=next(s for s in bundle["subscriptions"] if s["account_id"]==5)
        self.assertEqual(sub["plan_id"],4)
        self.assertEqual(sub["price_paid_minor"],1299)

    def test_ledger_gap_fails_closed(self):
        self.db.execute("UPDATE wallet_transactions SET balance_after_minor=1702 WHERE id=2")
        with self.assertRaisesRegex(portal.MigrationError, "continuity"):
            self.build()

    def test_missing_pepper_cannot_silently_revoke_grants(self):
        with patch.dict(os.environ, {"MGMA_TOKEN_PEPPER": ""}):
            with self.assertRaisesRegex(portal.MigrationError, "fingerprint"):
                self.build()

    def test_external_plans_preserve_metadata_and_stop_sales(self):
        self.db.execute("INSERT INTO plan_external_source_selections VALUES (1,4,19,'all')")
        bundle, _, future = self.build()
        self.assertTrue(bundle["plans"][0]["compatibility_pending"])
        self.assertFalse(bundle["plans"][0]["is_visible"])
        self.assertEqual(future["plan_external_source_selections"][0]["source_id"], 19)

    def test_next_plan_requires_stage3_companion(self):
        with self.assertRaisesRegex(portal.MigrationError, "pending next plan"):
            portal.convert(self.db, self.inputs[0], self.inputs[1], self.inputs[2], route_map=self.inputs[3], target_hosts=self.inputs[4], traffic_policy="preserve-total-as-download")

    def test_private_frozen_bundle_preserves_orphan_allocation_ids(self):
        from argparse import Namespace
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source.sqlite3"
            destination = sqlite3.connect(source)
            self.db.backup(destination)
            destination.close()
            paths = []
            for name, value in zip(("core", "mapping", "inventory", "routes", "hosts"), self.inputs):
                path = root / (name + ".json")
                path.write_bytes(portal.encode(value))
                paths.append(str(path))
            allocation = root / "allocation.sqlite3"
            db = sqlite3.connect(allocation)
            db.executescript("CREATE TABLE users(identity TEXT,mark INTEGER,address TEXT); CREATE TABLE state(k TEXT,v TEXT); CREATE TABLE owned(address TEXT); INSERT INTO users VALUES('99',1879048193,'2001:db8::99');")
            db.commit()
            db.close()
            output = root / "bundle"
            args = Namespace(traffic_policy="preserve-total-as-download", database=str(source), core=paths[0], inbound_map=paths[1], target_inbounds=paths[2], route_map=paths[3], target_hosts=paths[4], ipv6_allocation=["synthetic="+str(allocation)], output=str(output))
            result = portal.export(args)
            self.assertFalse(result["ready_for_cutover"])
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)
            for name, checksum in result["files"].items():
                member = output / name
                self.assertEqual(member.stat().st_mode & 0o777, 0o600)
                self.assertEqual(portal.digest(member.read_bytes()), checksum)
            imported = json.loads((output / "portal.json").read_text())
            self.assertEqual(imported["reserved_ids"], [7, 9, 99])
            self.assertEqual((output / "allocations/synthetic.sqlite3").read_bytes(), allocation.read_bytes())
            self.assertNotIn(LEGACY_KEY.encode(), (output / "portal.json").read_bytes())


if __name__ == "__main__":
    unittest.main()
