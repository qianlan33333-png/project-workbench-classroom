#!/bin/bash
# Binary rollback only. NEVER restore the old database over new user records.
set -euo pipefail
snapshot=${1:?Pass the rollback-today snapshot directory}
case "$snapshot" in /opt/project-workbench/rollback-today-*) ;; *) exit 2;; esac
previous=$(cat "$snapshot/previous-release")
test -x "$previous/bin/workbench"
python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
assert c.execute("SELECT count(*) FROM project_locks").fetchone()[0]==0, 'Wait for active work'
assert c.execute("SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')").fetchone()[0]==0
PY
systemctl stop project-workbench-channels project-workbench
# Protect the reserved personal space from being opened as an ordinary
# unrestricted project by the previous release. New tables/records remain intact.
python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
c.execute("UPDATE projects SET archived=1,data=json_set(data,'$.archived',json('true')) WHERE id='personal-assistant'")
c.commit()
PY
ln -sfn "$previous" /opt/project-workbench/current.today-rollback
mv -Tf /opt/project-workbench/current.today-rollback /opt/project-workbench/current
systemctl start project-workbench project-workbench-channels
curl -fsS --retry 20 --retry-delay 1 --retry-connrefused http://127.0.0.1:3082/api/v1/health
printf '\nNew task, memory, decision, context tables and forgotten-memories ledger were preserved.\n'
