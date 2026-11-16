#!/usr/bin/env python3
"""XUI_XRAY_LAUNCHER: REAL_XRAY -c FILE -> guarded core startup via stdin."""

import json
import fcntl
import os
from pathlib import Path
import subprocess
import sys
import tempfile

if __package__:
    from .policy import CONTROL, render, snapshot
else:
    from policy import CONTROL, render, snapshot


def atomic_json(path, value):
    path = Path(path)
    with tempfile.NamedTemporaryFile(dir=path.parent, mode="w", delete=False) as file:
        os.chmod(file.name, 0o600)
        json.dump(value, file, ensure_ascii=False)
        file.flush()
        os.fsync(file.fileno())
    os.replace(file.name, path)


def prepare(config, settings, pid):
    state = Path(settings["state"])
    if not state.is_absolute() or state.is_symlink():
        raise ValueError("invalid egress state directory")
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(state, 0o700)
    api = config.get("api") or {}
    if not api.get("tag") or api.get("listen") or any(i.get("tag") == CONTROL for i in config["inbounds"]):
        raise ValueError("egress needs the panel API outbound and a free control tag")
    api["services"] = list(dict.fromkeys([*api.get("services", []), "RoutingService", "HandlerService"]))
    config["api"] = api
    base = snapshot(config)
    base["routing"]["rules"].insert(0, {"type": "field", "inboundTag": [CONTROL], "outboundTag": api["tag"]})
    base["inbounds"][CONTROL] = "tunnel"
    base["pid"] = pid
    config["inbounds"].append({"tag": CONTROL, "listen": str(state / "control.sock"),
                                "protocol": "tunnel", "settings": {"rewriteAddress": "127.0.0.1"}})
    # A restart never trusts an old ready cache or an old kernel NAT table.
    # The controller opens authenticated users only after provisioning and readback.
    enabled = settings["enabled"]
    if type(enabled) is not bool:
        raise ValueError("enabled must be explicitly set")
    rules, extra = render(base, settings["inbounds"] if enabled else [], [])
    config["routing"] = dict(base["routing"], rules=rules)
    config["outbounds"].extend(extra)
    atomic_json(state / "base.json", base)
    atomic_json(state / "status.json", {"enabled": enabled, "verified": False,
                                       "pid": pid, "reason": "awaiting_reconcile", "users": []})
    return config


def main(argv=None):
    args = sys.argv[1:] if argv is None else argv
    if len(args) != 3 or args[1] != "-c":
        raise ValueError("launcher expects REAL_XRAY -c CONFIG")
    binary, _, path = args
    os.umask(0o077)
    filename = Path(os.environ["XUI_EGRESS_CONFIG"])
    settings = json.loads(filename.read_text())
    if Path(binary).resolve() != Path(settings["binary"]).resolve():
        raise ValueError("the installed binary must match the panel core")
    with (filename.parent / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        execute(binary, path, settings)


def execute(binary, path, settings):
    with open(path) as file:
        config = prepare(json.load(file), settings, os.getpid())
    raw = json.dumps(config).encode()
    checked = subprocess.run([binary, "run", "-test", "-config", "stdin:"], input=raw,
                             capture_output=True, timeout=25, check=False)
    if checked.returncode:
        raise RuntimeError("guarded core configuration was rejected")
    (Path(settings["state"]) / "control.sock").unlink(missing_ok=True)
    with tempfile.TemporaryFile() as file:
        file.write(raw)
        file.seek(0)
        os.dup2(file.fileno(), 0)
    # The caller keeps the lock until exec; Python's non-inheritable FD releases it.
    os.execv(binary, [binary, "run", "-config", "stdin:"])


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Core diagnostics and JSON can contain proxy credentials.
        print("3x-ui egress startup failed; inspect private configuration", file=sys.stderr)
        sys.exit(1)
