"""Collect one version of the original profile directory through the existing SSH source."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

from source_common import write_json
from source_ssh import connection

config_path = Path(sys.argv[1])
run_id = sys.argv[2]
if not config_path.is_absolute() or config_path.stat().st_mode & 0o077 or not re.fullmatch(r"[A-Za-z0-9_-]{1,96}", run_id):
    raise ValueError("invalid_directory_source_configuration")
settings = json.loads(config_path.read_text())
base = Path(settings["outputRoot"])
if not base.is_absolute():
    raise ValueError("directory_output_root_must_be_absolute")
root = base / run_id
root.mkdir(exist_ok=True, mode=0o700)
if root.is_symlink():
    raise ValueError("invalid_directory_output")
target = root / "profile-directory.jsonl"
manifest = root / "native-result.json"

with (root / ".collection.lock").open("a") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    if not manifest.is_file():
        script = Path(__file__).with_name("source_export_directory.py").read_text()
        args, env, host = connection(settings["contextSSH"])
        partial = target.with_suffix(".partial")
        try:
            with partial.open("wb") as stream:
                os.fchmod(stream.fileno(), 0o600)
                proc = subprocess.run(args + [host, "TZ=Asia/Shanghai nice -n 10 python3 -B -"],
                                      input=script.encode(), env=env, stdout=stream, stderr=subprocess.PIPE,
                                      timeout=900, check=False)
            if proc.returncode:
                raise ValueError("original_directory_export_failed")
            receipt = json.loads(proc.stderr.splitlines()[-1])
            with partial.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            if receipt["sha256"] != digest or receipt["count"] < 1 or not receipt["snapshot"]:
                raise ValueError("original_directory_manifest_mismatch")
            partial.replace(target)
            write_json(manifest, {"directory": str(root), "files": {
                "profile-directory": {"sha256": digest, "count": receipt["count"]}}, "report": receipt})
        finally:
            partial.unlink(missing_ok=True)
    print(manifest.read_text())
