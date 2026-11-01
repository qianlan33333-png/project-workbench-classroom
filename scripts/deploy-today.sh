#!/bin/bash
# Run on the workbench host as root after isolated acceptance.
set -euo pipefail
base=/opt/project-workbench
release=$base/releases/${PW_RELEASE:-cloud-20261101-13-today-inbox}
previous=$(readlink -f "$base/current")
backup=$base/rollback-today-$(date +%Y%m%d-%H%M%S)
test "$previous" != "$release"
test -x "$release/bin/workbench"
# No user or tool operations may be interrupted by this release.
python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
assert c.execute("SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')").fetchone()[0]==0, 'Wait for active tasks'
assert c.execute("SELECT count(*) FROM project_locks WHERE run_id NOT LIKE 'maintenance:%'").fetchone()[0]==0, 'Wait for business write locks'
assert c.execute("SELECT count(*) FROM tool_jobs WHERE state IN ('queued','running')").fetchone()[0]==0, 'Wait for tool operations'
PY
uid=$(id -u projectwb)
sudo -u projectwb env XDG_RUNTIME_DIR=/run/user/$uid DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus systemctl --user stop pwb-today-preview
systemctl stop project-workbench-channels
rollback(){
 ln -sfn "$previous" "$base/current.today-rollback"
 mv -Tf "$base/current.today-rollback" "$base/current"
 systemctl restart project-workbench
 systemctl start project-workbench-channels
}
trap rollback ERR
# Recheck after stopping consumers; receipt handling stays idempotent across restart.
python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
assert c.execute("SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')").fetchone()[0]==0
PY
systemctl stop project-workbench
mkdir -m 700 "$backup"
cp -a /var/lib/project-workbench "$backup/data"
cp -a /etc/project-workbench "$backup/config"
printf '%s\n' "$previous" > "$backup/previous-release"
ln -sfn "$release" "$base/current.today-next"
mv -Tf "$base/current.today-next" "$base/current"
systemctl start project-workbench
healthy=0
for n in $(seq 1 30); do
 if curl -fsS http://127.0.0.1:3082/api/v1/health >/dev/null; then healthy=1; break; fi
 sleep 1
done
test "$healthy" = 1
systemctl start project-workbench-channels
systemctl is-active project-workbench project-workbench-channels
printf 'release=%s\nbackup=%s\n' "$release" "$backup"
trap - ERR
