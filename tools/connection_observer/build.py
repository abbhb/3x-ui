#!/usr/bin/env python3
"""Build the repo-pinned Xray with an opt-in observer. No install or restart."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


SIGNATURE = "func DialSystem(ctx context.Context, dest net.Destination, sockopt *SocketConfig) (net.Conn, error) {"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    if output.exists() or output.with_suffix(output.suffix + ".json").exists():
        parser.error("output and manifest must be new files")
    repo = Path(__file__).resolve().parents[2]
    toolchain = subprocess.check_output(["go", "env", "GOVERSION"], cwd=repo, text=True).strip()
    info = json.loads(subprocess.check_output(["go", "list", "-m", "-json", "github.com/xtls/xray-core"], cwd=repo))
    downloaded = json.loads(subprocess.check_output(["go", "mod", "download", "-json", info["Path"] + "@" + info["Version"]], cwd=repo))
    observer = Path(__file__).with_name("observer.go.tmpl").read_bytes()
    with tempfile.TemporaryDirectory(prefix="3x-ui-observer-build-") as temporary:
        source = Path(temporary) / "xray"
        shutil.copytree(downloaded["Dir"], source)
        for directory in [source, *(p for p in source.rglob("*") if p.is_dir())]:
            directory.chmod(directory.stat().st_mode | 0o700)
        # Module cache files are read-only; the private copy is the only tree patched.
        dialer = source / "transport/internet/dialer.go"
        original = dialer.read_text()
        if original.count(SIGNATURE) != 1 or "xuiOriginalDialSystem" in original:
            parser.error("pinned Xray dialer signature changed; review before building")
        dialer.chmod(0o600)
        dialer.write_text(original.replace(SIGNATURE, SIGNATURE.replace("DialSystem(", "xuiOriginalDialSystem(")))
        (dialer.parent / "xui_observer.go").write_bytes(observer)
        output.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["go", "build", "-trimpath", "-o", str(output), "./main"], cwd=source,
                       env={**os.environ, "GOTOOLCHAIN": toolchain}, check=True)
    manifest = {"module": info["Path"], "version": info["Version"], "module_sum": downloaded["Sum"],
                "dialer_sha256": hashlib.sha256(original.encode()).hexdigest(),
                "observer_sha256": hashlib.sha256(observer).hexdigest(),
                "binary_sha256": hashlib.sha256(output.read_bytes()).hexdigest(),
                **json.loads(subprocess.check_output(["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION"], cwd=repo))}
    output.with_suffix(output.suffix + ".json").write_text(json.dumps(manifest, indent=2) + "\n")
    print("Built opt-in observer for pinned Xray " + info["Version"])


if __name__ == "__main__":
    main()
