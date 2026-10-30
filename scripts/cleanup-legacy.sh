#!/bin/bash
set -euo pipefail
# Only this server's retired workbench deployments; never company website or Node shared distribution.
python3 - <<'PY'
import json,time,pathlib
p=pathlib.Path('/opt/project-workbench/cutover.json')
v=json.loads(p.read_text())
if time.time() < v['at']+7*24*3600: raise SystemExit('7-day rollback retention has not elapsed')
PY
for unit in yihaowei-workbench deepseek-harness workbench-wechat workbench-feishu workbench-development; do
 if systemctl is-active --quiet "$unit"; then echo "Still active: $unit"; exit 1; fi
done
systemctl is-active --quiet project-workbench
systemctl is-active --quiet project-workbench-channels
curl --fail --silent http://127.0.0.1:3082/api/v1/health >/dev/null
python3 - <<'PY'
import sqlite3,json,time
s=sqlite3.connect('file:/var/lib/project-workbench/workbench.sqlite?mode=ro',uri=True)
if s.execute("SELECT count(*) FROM runs WHERE state='stopping'").fetchone()[0]:raise SystemExit('Unresolved cleanup; retain rollback')
for(raw,) in s.execute('SELECT data FROM connections'):
 c=json.loads(raw)
 if c.get('enabled') and (c.get('status')!='connected' or c.get('lastHeartbeat',0)<time.time()*1000-180000):raise SystemExit('Channel health unconfirmed; retain rollback')
PY
if [[ ${1:-} != --confirmed-stable ]]; then
 echo 'Retention and health gates passed. Review stability before --confirmed-stable. Snapshot and legacy data will remain.'
 exit 0
fi
# Explicit deployments only: preserve legacy data/snapshot for subsequent manual export.
for directory in /opt/yihaowei-workbench /opt/deepseek-harness /opt/workbench-development; do
 [[ ! -L "$directory" ]] || { echo "Unexpected symlink: $directory"; exit 1; }
 rm -rf --one-file-system -- "$directory"
done
echo 'Retired deployment directories removed; rollback snapshot, old data and company site retained.'
