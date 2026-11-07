#!/usr/bin/env python3
"""Start one isolated acceptance service. Private settings are never printed."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.request

root = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser()
parser.add_argument("service", choices=("host", "worker"))
args = parser.parse_args()
private = root / ".var/native-acceptance"
settings = json.loads((private / "credentials.json").read_text())
env = dict(os.environ, WORKBENCH_HOST_TOKEN=settings["hostToken"],
           WORKBENCH_MYSQL_DSN=f'{settings["user"]}:{settings["password"]}@tcp(127.0.0.1:13307)/{settings["database"]}',
           TEMPORAL_ADDRESS="127.0.0.1:17233")
if args.service == "host":
    urllib.request.urlopen("http://127.0.0.1:7381/health", timeout=5).close()
    env.update(json.loads((private / "model-key.json").read_text()))
    env.update(DSH_HOME=str(root / ".var/native-home"),
               WORKBENCH_PERSONAL_WORKSPACE=str(private / "today"))
    command = ["node", str(root / ".var/native-runtime/node_modules/@deepseek-ai/dsh/lib/bin.js"),
               "web", "--patch", str(root / "native/harness/cordis.patch.yml"),
               "--no-open", "--host", "127.0.0.1", "--port", "7380"]
    cwd = root
else:
    command = ["go", "run", "./cmd/worker"]
    cwd = root / "native/worker"
log_path = private / ("harness.log" if args.service == "host" else "worker.log")
fd = os.open(log_path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
print(f"Starting isolated {args.service}; diagnostic log: {log_path.name}", flush=True)
with os.fdopen(fd, "w") as output:
    child = subprocess.Popen(command, cwd=cwd, env=env, stdout=output, stderr=output)
    try:
        sys.exit(child.wait())
    except KeyboardInterrupt:
        child.wait(timeout=20)
