from contextlib import closing
from copy import deepcopy
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from tools.egress import launch, manage, policy


def core_config():
    return {"api": {"tag": "api", "services": ["HandlerService"]},
            "inbounds": [{"tag": "FIXED_V6_ENTRY", "listen": "2001:db8:1::a", "port": 443,
                          "protocol": "vless", "settings": {"clients": []}},
                         {"tag": "ORDINARY", "protocol": "hysteria", "port": 22443}],
            "outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {
                "finalRules": [{"action": "block", "ip": ["geoip:private"]}, {"action": "allow"}]}},
                          {"tag": "blocked", "protocol": "blackhole"},
                          {"tag": "wg", "protocol": "wireguard"}],
            "routing": {"domainStrategy": "AsIs", "rules": [
                {"type": "field", "ip": ["geoip:private"], "outboundTag": "blocked"},
                {"type": "field", "inboundTag": ["ORDINARY"], "outboundTag": "wg"}]}}


class PolicyTests(unittest.TestCase):
    def test_default_direct_preserves_its_security_settings_and_prior_routes(self):
        original = core_config()
        base = policy.snapshot(original)
        rules, outbounds = policy.render(base, ["FIXED_V6_ENTRY"], [{"email": "7.sample", "mark": 123}])
        self.assertEqual(rules[:2], original["routing"]["rules"])
        self.assertEqual([r["network"] for r in rules[2:4]], ["tcp", "udp"])
        self.assertEqual(rules[-1]["outboundTag"], "mz6-pending")
        self.assertEqual(outbounds[0]["settings"], original["outbounds"][0]["settings"])
        self.assertEqual(outbounds[0]["streamSettings"]["sockopt"]["mark"], 123)
        self.assertTrue(outbounds[0]["streamSettings"]["sockopt"]["happyEyeballs"]["prioritizeIPv6"])
        self.assertEqual(outbounds[1]["streamSettings"]["sockopt"]["domainStrategy"], "ForceIPv6v4")
        self.assertEqual(original, core_config())

    def test_never_turns_a_tunnel_or_redirect_into_a_user_direct_exit(self):
        for mutation in ("tunnel", "redirect", "existing_mark", "balancer"):
            with self.subTest(mutation=mutation):
                config = core_config()
                if mutation == "tunnel":
                    config["routing"]["rules"].append({"inboundTag": ["FIXED_V6_ENTRY"], "outboundTag": "wg"})
                elif mutation == "redirect":
                    config["outbounds"][0]["settings"]["redirect"] = "example.test:443"
                elif mutation == "existing_mark":
                    config["outbounds"][0]["streamSettings"] = {"sockopt": {"mark": 99}}
                else:
                    config["routing"]["balancers"] = [{"tag": "b"}]
                with self.assertRaises(ValueError):
                    policy.render(policy.snapshot(config), ["FIXED_V6_ENTRY"], [])

    def test_legacy_identity_ignores_username_but_not_user_number(self):
        self.assertEqual(policy.identity("7.old-name"), policy.identity("7.new-name"))
        self.assertIsNone(policy.identity("ordinary-native-client"))
        self.assertIsNone(policy.identity("0.invalid"))


class ControllerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = self.root / "state"
        self.settings = {"namespace": "lab", "state": str(self.state), "binary": str(self.root / "xray"),
                         "allocations": str(self.root / "allocations.sqlite3"), "interface": "veth0",
                         "prefix": "2001:db8:1::/64", "mark_prefix": 25903,
                         "enabled": True, "inbounds": ["FIXED_V6_ENTRY"]}
        self.path = self.root / "node.json"
        self.path.write_text(json.dumps(self.settings))
        prepared = launch.prepare(core_config(), self.settings, 1234)
        self.assertEqual(prepared["inbounds"][0]["listen"], "2001:db8:1::a")
        self.assertEqual(prepared["inbounds"][0]["port"], 443)
        (self.state / "control.sock").touch()
        self.live_rules = prepared["routing"]["rules"]
        self.outbounds = prepared["outbounds"]
        self.emails = ["7.sample", "9.sample", "unknown-name"]
        self.calls = []
        with closing(sqlite3.connect(self.settings["allocations"])) as db, db:
            db.executescript("""CREATE TABLE users(identity TEXT PRIMARY KEY,mark INTEGER UNIQUE,address TEXT UNIQUE);
                CREATE TABLE state(key TEXT PRIMARY KEY,value TEXT);
                CREATE TABLE owned(address TEXT PRIMARY KEY);""")
            for key in (7, 11):
                db.execute("INSERT INTO users VALUES (?,?,?)", (str(key), (25903 << 16) | key, f"2001:db8:1::{key}"))
                db.execute("INSERT INTO owned VALUES (?)", (f"2001:db8:1::{key}",))
            db.execute("INSERT INTO state VALUES ('mark_prefix','25903')")
            db.execute("INSERT INTO state VALUES ('legacy_note','\"preserve-me\"')")
        self.inst = manage.Instance(self.path, lab=True)
        self.addCleanup(self.inst.db.close)
        self.inst.addresses = lambda: {"2001:db8:1::a": {"prefixlen": 64}}
        self.inst.provision = lambda rows: self.calls.append(("provision", deepcopy(rows)))
        self.inst.nat = lambda rows: self.calls.append(("nat", deepcopy(rows)))
        self.inst.api = self.api

    def api(self, operation, *args, data=None, reply=False):
        self.calls.append((operation, deepcopy(data)))
        if operation == "inbounduser":
            return {"users": [{"email": email} for email in self.emails]}
        if operation == "lsrules":
            return {"rules": [{"ruleTag": r.get("ruleTag", ""), "tag": r.get("outboundTag", "")} for r in self.live_rules]}
        if operation == "lso":
            return {"outbounds": self.outbounds}
        if operation == "ado":
            self.outbounds.extend(data["outbounds"])
        if operation == "adrules":
            self.live_rules = data["routing"]["rules"]
        if operation == "rmo":
            self.outbounds = [o for o in self.outbounds if o["tag"] != args[0]]

    def test_restart_guard_opens_only_after_provision_nat_and_readback(self):
        self.assertTrue(any(r.get("outboundTag") == "mz6-pending" for r in self.live_rules))
        status = self.inst.reconcile()
        operations = [row[0] for row in self.calls]
        self.assertLess(operations.index("provision"), operations.index("nat"))
        self.assertLess(operations.index("nat"), operations.index("adrules"))
        self.assertTrue(status["verified"])
        self.assertEqual(status["blockedUsers"], ["unknown-name"])
        self.assertEqual(status["users"][0], {"identity": "7", "address": "2001:db8:1::7"})
        self.assertEqual(self.inst.value("legacy_note"), "preserve-me")
        orphan = self.inst.db.execute("SELECT address FROM users WHERE identity='11'").fetchone()[0]
        self.assertEqual(orphan, "2001:db8:1::11")
        assigned = self.inst.allocate("9")
        self.emails[1] = "9.renamed"
        self.inst.reconcile()
        self.assertEqual(self.inst.allocate("9"), assigned)

    def test_off_keeps_allocation_and_fixed_entry(self):
        self.inst.reconcile()
        address = self.inst.allocate("9")
        self.settings["enabled"] = False
        self.path.write_text(json.dumps(self.settings))
        self.inst.config = self.settings
        status = self.inst.reconcile()
        self.assertFalse(status["enabled"])
        self.assertTrue(status["verified"])
        self.assertEqual(self.inst.allocate("9"), address)
        self.assertIn(("nat", []), self.calls)
        self.assertFalse(any(r.get("ruleTag", "").startswith("mz6-") for r in self.live_rules))

    def test_reset_requires_current_address_and_preserves_mark_and_reservation(self):
        before = self.inst.allocate("7")
        with self.assertRaisesRegex(ValueError, "allocation changed"):
            self.inst.reset("7", "2001:db8:1::bad")
        self.assertEqual(self.inst.allocate("7"), before)
        after = self.inst.reset("7", before["address"])
        self.assertEqual(before["mark"], after["mark"])
        self.assertNotEqual(before["address"], after["address"])
        self.assertIsNotNone(self.inst.db.execute("SELECT 1 FROM owned WHERE address=?", (before["address"],)).fetchone())

    def test_provision_failure_never_opens_users_or_changes_routes(self):
        self.inst.provision = lambda rows: (_ for _ in ()).throw(RuntimeError("probe failed"))
        before = deepcopy(self.live_rules)
        with self.assertRaises(RuntimeError):
            self.inst.reconcile()
        self.assertEqual(self.live_rules, before)
        self.assertNotIn("nat", [c[0] for c in self.calls])

    def test_foreign_route_or_core_generation_change_is_refused(self):
        self.live_rules.append({"ruleTag": "other-controller", "outboundTag": "blocked"})
        with self.assertRaisesRegex(RuntimeError, "base routing changed"):
            self.inst.reconcile()
        self.live_rules.pop()
        def restart(_):
            base = json.loads((self.state / "base.json").read_text())
            base["pid"] = 9999
            launch.atomic_json(self.state / "base.json", base)
        self.inst.provision = restart
        with self.assertRaisesRegex(RuntimeError, "changed during"):
            self.inst.reconcile()
        self.assertNotIn("adrules", [c[0] for c in self.calls])

    def test_duplicate_permanent_identity_is_never_shared(self):
        self.emails = ["7.alice", "7.someone-else"]
        with self.assertRaisesRegex(RuntimeError, "same permanent"):
            self.inst.reconcile()
        self.assertNotIn("adrules", [c[0] for c in self.calls])

    def test_literal_nft_script_changes_only_owned_ipv6_nat(self):
        calls = []
        def fake(args, data=None):
            calls.append((args, data))
            if args == ["nft", "-j", "list", "tables"]:
                return '{"nftables":[]}'
            return '{"nftables":[{"table":{"family":"ip6","name":"mz6_lab"}}]}'
        rows = [{"identity": "7", "mark": (25903 << 16) | 7, "address": "2001:db8:1::7"}]
        with patch.object(manage, "command", fake):
            manage.Instance.nat(self.inst, rows)
        script = next(data for args, data in calls if args == ["nft", "-f", "-"])
        self.assertIn("ip6 mz6_lab", script)
        self.assertIn("snat to meta mark map @users", script)
        self.assertNotIn("delete address", script)
        self.assertNotIn("add table ip ", script)

    def test_native_file_launcher_compiles_stdin_and_checks_actual_binary(self):
        path = self.root / "core.json"
        path.write_text(json.dumps(core_config()))
        with patch.dict("os.environ", {"XUI_EGRESS_CONFIG": str(self.path)}), \
                patch.object(launch.subprocess, "run") as check, patch.object(launch.os, "dup2"), \
                patch.object(launch.os, "execv") as execute:
            check.return_value.returncode = 0
            launch.main([self.settings["binary"], "-c", str(path)])
            raw = check.call_args.kwargs["input"]
            self.assertIn("mz6-pending", raw.decode())
            execute.assert_called_once_with(self.settings["binary"], [self.settings["binary"], "run", "-config", "stdin:"])
            with self.assertRaisesRegex(ValueError, "installed binary"):
                launch.main([str(self.root / "different-core"), "-c", str(path)])


if __name__ == "__main__":
    unittest.main()
