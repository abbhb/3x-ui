#!/usr/bin/env python3
"""Temporary SQLite usage handoff while a protected legacy core stays online.

Both panels retain their own atomic traffic increments. This tool transfers only
new usage, with a receipt in each database, and mirrors native usage into the
legacy quota counter. It never edits credentials, limits, routing, or services.
Commerce and resets must remain frozen until the legacy service is retired.
"""
import argparse
from contextlib import closing, contextmanager
import json
from pathlib import Path
import re
import sqlite3
import sys
import time


SCHEMA = "marzban-live-usage-handoff/v1"
MAX = 2**63 - 1


class HandoffError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise HandoffError(message)


def counter(value):
    require(type(value) is int and 0 <= value <= MAX, "invalid traffic counter")
    return value


@contextmanager
def connection(path, write=False):
    path = Path(path).resolve(strict=True)
    with closing(sqlite3.connect(path.as_uri() + ("?mode=rw" if write else "?mode=ro"), uri=True, timeout=15, isolation_level=None)) as db:
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA busy_timeout=15000")
        if write:
            db.execute("PRAGMA synchronous=FULL")
            db.execute("BEGIN IMMEDIATE")
        else:
            db.execute("BEGIN")
        try:
            yield db
            db.execute("COMMIT")
        except BaseException:
            db.execute("ROLLBACK")
            raise


def metadata(db, digest):
    db.execute("CREATE TABLE IF NOT EXISTS xui_handoff_meta (id INTEGER PRIMARY KEY CHECK(id=1), schema TEXT NOT NULL, bundle_sha256 TEXT NOT NULL)")
    row = db.execute("SELECT schema,bundle_sha256 FROM xui_handoff_meta WHERE id=1").fetchone()
    if row:
        require(row["schema"] == SCHEMA and row["bundle_sha256"] == digest, "different handoff already initialized")
    else:
        db.execute("INSERT INTO xui_handoff_meta VALUES(1,?,?)", (SCHEMA, digest))


def source_state(db, ident):
    row = db.execute("SELECT id,username,used_traffic,data_limit_reset_strategy FROM users WHERE id=?", (ident,)).fetchone()
    require(row is not None and row["data_limit_reset_strategy"] == "no_reset", "source user missing or reset policy changed")
    history = counter(db.execute("SELECT COALESCE(SUM(used_traffic_at_reset),0) FROM user_usage_logs WHERE user_id=?", (ident,)).fetchone()[0])
    return row, counter(counter(row["used_traffic"]) + history)


def target_state(db, ident, email):
    row = db.execute("SELECT id,email,reset,reset_day,reset_weekday,traffic_reset FROM clients WHERE id=?", (ident,)).fetchone()
    require(row is not None and row["email"] == email and row["reset"] == 0 and row["reset_day"] == 0 and row["reset_weekday"] == 0 and row["traffic_reset"] == "never", "target identity or reset policy changed")
    traffic = db.execute("SELECT up,down,reset_count FROM client_traffics WHERE email=?", (email,)).fetchone()
    require(traffic is not None and traffic["reset_count"] == 0, "target usage was reset or removed")
    return counter(counter(traffic["up"]) + counter(traffic["down"]))


def initialize(source, target, baseline, offline=False):
    require(offline, "initialization requires an offline target")
    require(Path(source).resolve() != Path(target).resolve(), "source and target must differ")
    require(baseline.get("schema") == SCHEMA and re.fullmatch(r"[a-f0-9]{64}", baseline.get("bundle_sha256", "")) is not None, "invalid frozen baseline")
    users = baseline.get("users", [])
    require(0 < len(users) <= 10000, "invalid baseline users")
    require(len({u["source_id"] for u in users}) == len(users), "duplicate baseline identity")
    # Target is still offline. A failed initialization can be retried with the
    # same immutable baseline; inserts never overwrite progress or receipts.
    with connection(source, True) as src:
        metadata(src, baseline["bundle_sha256"])
        src.execute("CREATE TABLE IF NOT EXISTS xui_handoff_mirrors (user_id INTEGER PRIMARY KEY, native_bytes INTEGER NOT NULL CHECK(native_bytes>=0), updated_at INTEGER NOT NULL)")
        require(src.execute("SELECT COUNT(*) FROM next_plans").fetchone()[0] == 0, "pending source plans require separate reconciliation")
        for user in users:
            ident = counter(user["source_id"])
            row, gross = source_state(src, ident)
            require(ident > 0 and user["email"] == str(ident) + "." + row["username"].lower(), "source identity mismatch")
            require(gross >= counter(user["source_lifetime"]), "source baseline regressed")
            src.execute("INSERT OR IGNORE INTO xui_handoff_mirrors VALUES(?,0,?)", (ident, int(time.time())))
    with connection(target, True) as dst:
        metadata(dst, baseline["bundle_sha256"])
        dst.execute("CREATE TABLE IF NOT EXISTS xui_handoff_receipts (user_id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, source_lifetime INTEGER NOT NULL, target_usage INTEGER NOT NULL, legacy_bytes INTEGER NOT NULL CHECK(legacy_bytes>=0), updated_at INTEGER NOT NULL)")
        for user in users:
            used = target_state(dst, user["source_id"], user["email"])
            old = dst.execute("SELECT * FROM xui_handoff_receipts WHERE user_id=?", (user["source_id"],)).fetchone()
            if old:
                require(old["email"] == user["email"] and old["source_lifetime"] == user["source_lifetime"] and old["target_usage"] == user["target_usage"], "different target baseline")
            else:
                require(used == counter(user["target_usage"]), "target already counted traffic before initialization")
                dst.execute("INSERT INTO xui_handoff_receipts VALUES(?,?,?,?,0,?)", (user["source_id"], user["email"], counter(user["source_lifetime"]), used, int(time.time())))
    return {"users": len(users), "initialized": True}


def synchronize(source, target, after_commit=None):
    require(Path(source).resolve() != Path(target).resolve(), "source and target must differ")
    with connection(target) as dst:
        meta = dst.execute("SELECT * FROM xui_handoff_meta WHERE id=1").fetchone()
        require(meta is not None and meta["schema"] == SCHEMA, "target not initialized")
        receipts = [dict(r) for r in dst.execute("SELECT * FROM xui_handoff_receipts ORDER BY user_id")]
    require(receipts, "empty handoff")
    observed = {}
    with connection(source) as src:
        source_meta = src.execute("SELECT * FROM xui_handoff_meta WHERE id=1").fetchone()
        require(source_meta is not None and source_meta["bundle_sha256"] == meta["bundle_sha256"], "handoff mismatch")
        for receipt in receipts:
            _, gross = source_state(src, receipt["user_id"])
            mirror = src.execute("SELECT native_bytes FROM xui_handoff_mirrors WHERE user_id=?", (receipt["user_id"],)).fetchone()
            require(mirror is not None, "source mirror missing")
            observed[receipt["user_id"]] = counter(gross - counter(mirror[0]))
    native = {}
    legacy_delta = 0
    with connection(target, True) as dst:
        for initial in receipts:
            receipt = dst.execute("SELECT * FROM xui_handoff_receipts WHERE user_id=?", (initial["user_id"],)).fetchone()
            require(receipt is not None, "target receipt removed")
            used = target_state(dst, receipt["user_id"], receipt["email"])
            old_usage = counter(observed[receipt["user_id"]] - receipt["source_lifetime"])
            delta = counter(old_usage - receipt["legacy_bytes"])
            counter(used + delta)
            # Increment and receipt commit together; another panel's atomic
            # increments commute with this update without lost usage.
            dst.execute("UPDATE client_traffics SET down=down+? WHERE email=?", (delta, receipt["email"]))
            dst.execute("UPDATE xui_handoff_receipts SET legacy_bytes=?,updated_at=? WHERE user_id=?", (old_usage, int(time.time()), receipt["user_id"]))
            native[receipt["user_id"]] = counter(used + delta - receipt["target_usage"] - old_usage)
            legacy_delta += delta
    if after_commit:
        after_commit("target")
    native_delta = 0
    with connection(source, True) as src:
        for ident, counted in native.items():
            row, gross = source_state(src, ident)
            mirror = src.execute("SELECT native_bytes FROM xui_handoff_mirrors WHERE user_id=?", (ident,)).fetchone()
            require(mirror is not None, "source mirror removed")
            delta = counter(counted - counter(mirror[0]))
            counter(gross + delta)
            src.execute("UPDATE users SET used_traffic=used_traffic+? WHERE id=?", (delta, ident))
            src.execute("UPDATE xui_handoff_mirrors SET native_bytes=?,updated_at=? WHERE user_id=?", (counted, int(time.time()), ident))
            native_delta += delta
    if after_commit:
        after_commit("source")
    return {"users": len(receipts), "legacy_bytes_added": legacy_delta, "native_bytes_mirrored": native_delta}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["initialize", "sync"])
    parser.add_argument("--source", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--baseline")
    parser.add_argument("--offline-target", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.command == "initialize":
            require(args.baseline is not None, "frozen baseline required")
            report = initialize(args.source, args.target, json.loads(Path(args.baseline).read_text()), args.offline_target)
        else:
            report = synchronize(args.source, args.target)
        print(json.dumps(report))
        return 0
    except (HandoffError, sqlite3.Error, ValueError, OSError, KeyError, TypeError):
        print("legacy usage handoff stopped; verify the frozen identities, unchanged quotas/reset policies, receipts and database availability", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
