#!/bin/bash
# Release 09 has already passed isolated preview acceptance.
set -euo pipefail
base=/opt/project-workbench
release=$base/releases/cloud-20261030-09-composer
previous=$(readlink -f "$base/current")
backup=$base/rollback-composer-20261030
test "$previous" = "$base/releases/cloud-20261030-08-lifecycle-tools"
test -x "$release/bin/workbench"
test ! -e "$backup"
uid=$(id -u projectwb)
sudo -u projectwb env XDG_RUNTIME_DIR=/run/user/$uid DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus systemctl --user stop pwb-composer-preview
systemctl stop project-workbench-channels
if ! python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
assert c.execute("SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')").fetchone()[0]==0,'Wait for all active work'
assert c.execute("SELECT count(*) FROM tool_jobs WHERE state IN ('queued','running')").fetchone()[0]==0,'Wait for tool operations'
PY
then
 systemctl start project-workbench-channels
 exit 1
fi
rollback(){
 ln -sfn "$previous" "$base/current.rollback"
 mv -Tf "$base/current.rollback" "$base/current"
 systemctl restart project-workbench
 systemctl start project-workbench-channels
}
trap rollback ERR
systemctl stop project-workbench
mkdir -m 700 "$backup"
cp -a /var/lib/project-workbench "$backup/data"
cp -a /etc/project-workbench "$backup/config"
printf '%s\n' "$previous" > "$backup/previous-release"
ln -sfn "$release" "$base/current.composer-next"
mv -Tf "$base/current.composer-next" "$base/current"
systemctl start project-workbench
healthy=0
for n in $(seq 1 30); do
 if curl -fsS http://127.0.0.1:3082/api/v1/health >/dev/null; then healthy=1; break; fi
 sleep 1
done
test "$healthy" = 1
systemctl start project-workbench-channels
systemctl is-active project-workbench project-workbench-channels
readlink "$base/current"
trap - ERR
