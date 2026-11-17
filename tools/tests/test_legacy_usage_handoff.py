import json
from contextlib import closing
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import legacy_usage_handoff as handoff


class LegacyUsageHandoffTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.source = Path(tmp.name) / "source.sqlite3"
        self.target = Path(tmp.name) / "target.sqlite3"
        for path in [self.source, self.target]:
            with closing(sqlite3.connect(path)) as db, db:
                db.execute("PRAGMA journal_mode=WAL")
        self.sql(self.source, "CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT, used_traffic INTEGER, data_limit_reset_strategy TEXT)")
        self.sql(self.source, "CREATE TABLE user_usage_logs(user_id INTEGER, used_traffic_at_reset INTEGER)")
        self.sql(self.source, "CREATE TABLE next_plans(user_id INTEGER)")
        self.sql(self.source, "INSERT INTO users VALUES(7,'synthetic',100,'no_reset')")
        self.sql(self.source, "INSERT INTO user_usage_logs VALUES(7,40)")
        self.sql(self.target, "CREATE TABLE clients(id INTEGER PRIMARY KEY,email TEXT,reset INTEGER,reset_day INTEGER,reset_weekday INTEGER,traffic_reset TEXT)")
        self.sql(self.target, "CREATE TABLE client_traffics(email TEXT PRIMARY KEY,up INTEGER,down INTEGER,reset_count INTEGER)")
        self.sql(self.target, "INSERT INTO clients VALUES(7,'7.synthetic',0,0,0,'never')")
        self.sql(self.target, "INSERT INTO client_traffics VALUES('7.synthetic',0,100,0)")
        self.baseline = {"schema": handoff.SCHEMA, "bundle_sha256": "a" * 64, "users": [{"source_id": 7, "email": "7.synthetic", "source_lifetime": 140, "target_usage": 100}]}

    def sql(self, path, statement):
        with closing(sqlite3.connect(path)) as db, db:
            cursor = db.execute(statement)
            return cursor.fetchall()

    def initialize(self):
        return handoff.initialize(self.source, self.target, self.baseline, offline=True)

    def counts(self):
        return (self.sql(self.source, "SELECT used_traffic FROM users")[0][0], self.sql(self.target, "SELECT up+down FROM client_traffics")[0][0])

    def test_live_increments_converge_without_feedback_or_lost_native_usage(self):
        self.initialize()
        self.sql(self.source, "UPDATE users SET used_traffic=used_traffic+30")
        self.sql(self.target, "UPDATE client_traffics SET up=up+20")
        self.assertEqual(handoff.synchronize(self.source, self.target), {"users": 1, "legacy_bytes_added": 30, "native_bytes_mirrored": 20})
        self.assertEqual(self.counts(), (150, 150))
        self.sql(self.source, "UPDATE users SET used_traffic=used_traffic+11")
        self.sql(self.target, "UPDATE client_traffics SET down=down+13")
        handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (174, 174))
        for _ in range(5):
            report = handoff.synchronize(self.source, self.target)
            self.assertEqual(report["legacy_bytes_added"], 0)
            self.assertEqual(report["native_bytes_mirrored"], 0)
        self.assertEqual(self.counts(), (174, 174))

    def test_crash_at_each_commit_replays_exactly_once(self):
        self.initialize()
        for phase in ["target", "source"]:
            self.sql(self.source, "UPDATE users SET used_traffic=used_traffic+30")
            self.sql(self.target, "UPDATE client_traffics SET up=up+20")
            def crash(committed):
                if committed == phase:
                    raise RuntimeError("synthetic crash")
            with self.assertRaises(RuntimeError):
                handoff.synchronize(self.source, self.target, after_commit=crash)
            handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (200, 200))

    def test_source_increment_between_commits_is_retained(self):
        self.initialize()
        self.sql(self.target, "UPDATE client_traffics SET up=up+20")
        def concurrent_increment(phase):
            if phase == "target":
                self.sql(self.source, "UPDATE users SET used_traffic=used_traffic+9")
        handoff.synchronize(self.source, self.target, after_commit=concurrent_increment)
        handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (129, 129))

    def test_source_reset_policy_change_stops_before_native_write(self):
        self.initialize()
        self.sql(self.source, "UPDATE users SET data_limit_reset_strategy='day',used_traffic=0")
        with self.assertRaises(handoff.HandoffError):
            handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (0, 100))

    def test_native_reset_stops_before_source_quota_mirror(self):
        self.initialize()
        self.sql(self.target, "UPDATE client_traffics SET up=20,reset_count=1")
        with self.assertRaises(handoff.HandoffError):
            handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (100, 120))

    def test_source_counter_regression_does_not_subtract_usage(self):
        self.initialize()
        self.sql(self.source, "UPDATE users SET used_traffic=90")
        with self.assertRaises(handoff.HandoffError):
            handoff.synchronize(self.source, self.target)
        self.assertEqual(self.counts(), (90, 100))

    def test_init_is_offline_and_cannot_replace_baseline_or_progress(self):
        with self.assertRaises(handoff.HandoffError):
            handoff.initialize(self.source, self.target, self.baseline)
        self.initialize()
        self.sql(self.source, "UPDATE users SET used_traffic=130")
        handoff.synchronize(self.source, self.target)
        self.initialize()
        self.assertEqual(self.counts(), (130, 130))
        changed = json.loads(json.dumps(self.baseline))
        changed["bundle_sha256"] = "b" * 64
        with self.assertRaises(handoff.HandoffError):
            handoff.initialize(self.source, self.target, changed, offline=True)


if __name__ == "__main__":
    unittest.main()
