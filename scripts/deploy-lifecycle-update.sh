#!/bin/bash
# Deploy this verified increment only. Run as root with the update archive.
set -euo pipefail
archive=$(realpath "${1:?Pass pwb-lifecycle-update.tar.gz}")
base=/opt/project-workbench
release=$base/releases/cloud-20261030-08-lifecycle-tools
previous=$(readlink -f "$base/current")
backup=$base/rollback-lifecycle-tools-20261030
test "$previous" = "$base/releases/cloud-20261030-07-session-titles"
test -d "$release"
test ! -e "$backup"
uid=$(id -u projectwb)
userctl() { sudo -u projectwb env XDG_RUNTIME_DIR=/run/user/$uid DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus systemctl --user "$@"; }
# This preview belongs to this release, never another application.
userctl stop pwb-lifecycle-preview.service || true
rm -rf "$release/web/dist"
tar -xzf "$archive" -C "$release"
chown -R projectwb:projectwb "$release"
systemctl stop project-workbench-channels
if ! python3 - <<'PY'
import sqlite3
c=sqlite3.connect('/var/lib/project-workbench/workbench.sqlite')
assert c.execute("SELECT count(*) FROM runs WHERE state IN ('queued','running','waiting','stopping')").fetchone()[0]==0,'Active work must finish before deploy'
if c.execute("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tool_jobs'").fetchone()[0]:
 assert c.execute("SELECT count(*) FROM tool_jobs WHERE json_extract(data,'$.state') IN ('queued','running')").fetchone()[0]==0,'Tool installation/auth still active'
PY
then
 systemctl start project-workbench-channels
 exit 1
fi
rollback() {
 echo 'Update failed; restoring previous program and configuration, keeping current data' >&2
 if test -d "$backup/config"; then cp -a "$backup/config/." /etc/project-workbench/; fi
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
if test -d "$release/tool-artifacts"; then
 install -d -o projectwb -g projectwb -m 700 /var/lib/project-workbench/tools /var/lib/project-workbench/tools/downloads
 cp -a "$release/tool-artifacts/." /var/lib/project-workbench/tools/downloads/
 chown -R projectwb:projectwb /var/lib/project-workbench/tools/downloads
fi
install -d -m 755 /usr/local/libexec
install -o root -g root -m 755 "$release/scripts/scrub-session-backups.py" /usr/local/libexec/pwb-purge-backups.py
printf '%s\n' 'projectwb ALL=(root) NOPASSWD: /usr/bin/python3 /usr/local/libexec/pwb-purge-backups.py' > /etc/sudoers.d/project-workbench-purge
chmod 440 /etc/sudoers.d/project-workbench-purge
visudo -cf /etc/sudoers.d/project-workbench-purge
python3 - <<'PY'
import json,pathlib
p=pathlib.Path('/etc/project-workbench/config.json');v=json.loads(p.read_text())
v['RuntimeDir']='/opt/project-workbench/current/runtime'
v['BackupScrubCommand']=['/usr/bin/sudo','-n','/usr/bin/python3','/usr/local/libexec/pwb-purge-backups.py']
p.write_text(json.dumps(v,ensure_ascii=False,indent=2));p.chmod(0o600)
PY
ln -sfn "$release" "$base/current.lifecycle-next"
mv -Tf "$base/current.lifecycle-next" "$base/current"
systemctl start project-workbench
healthy=0
for n in $(seq 1 30); do
 if curl -fsS http://127.0.0.1:3082/api/v1/health >/dev/null; then healthy=1;break;fi
 sleep 1
done
test "$healthy" = 1
systemctl start project-workbench-channels
systemctl is-active project-workbench project-workbench-channels
readlink "$base/current"
trap - ERR
