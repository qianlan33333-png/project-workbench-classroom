#!/bin/bash
set -euo pipefail
# Run as root. This installs only the new workbench, never edits Caddy or old apps.
archive=$(realpath "${1:?Usage: install-release.sh RELEASE.tar.gz RELEASE_NAME}")
release_name=${2:?Use a unique release name}
[[ $release_name =~ ^[a-zA-Z0-9._-]+$ ]] || exit 1
cd "$(dirname "$archive")"
sha256sum -c "$(basename "$archive").sha256"
id projectwb >/dev/null 2>&1 || useradd --create-home --shell /bin/bash projectwb
loginctl enable-linger projectwb
install -d -o projectwb -g projectwb -m 750 /opt/project-workbench/releases /var/lib/project-workbench /etc/project-workbench
release=/opt/project-workbench/releases/$release_name
test ! -e "$release"
mkdir "$release"
tar -xzf "$archive" -C "$release"
test "$("$release/node/bin/node" --version)" = v24.22.0
test -f "$release/node/include/node/node_api.h"
chown -R projectwb:projectwb "$release"
sudo -iu projectwb podman build --build-arg "APT_MIRROR=${APT_MIRROR:-deb.debian.org}" -t "localhost/project-workbench-runtime:$release_name" "$release"
sudo -iu projectwb podman run --rm --userns=keep-id "localhost/project-workbench-runtime:$release_name" dsh --version
test -f /etc/project-workbench/config.json || { echo 'Place configured deploy/config.example.json at /etc/project-workbench/config.json first.'; exit 1; }
# Config RuntimeImage must match the built release; preserve other settings.
python3 - "$release_name" <<'PY'
import json,pathlib,sys,os,pwd
p=pathlib.Path('/etc/project-workbench/config.json');v=json.loads(p.read_text());v['RuntimeImage']='localhost/project-workbench-runtime:'+sys.argv[1]
p.write_text(json.dumps(v,ensure_ascii=False,indent=2));u=pwd.getpwnam('projectwb');os.chown(p,u.pw_uid,u.pw_gid);os.chmod(p,0o600)
env=pathlib.Path('/etc/project-workbench/service.env');env.write_text(f'XDG_RUNTIME_DIR=/run/user/{u.pw_uid}\nDBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/{u.pw_uid}/bus\n');os.chown(env,u.pw_uid,u.pw_gid);os.chmod(env,0o600)
PY
systemctl stop project-workbench-channels.service project-workbench.service 2>/dev/null || true
ln -s "$release" /opt/project-workbench/current.next
mv -Tf /opt/project-workbench/current.next /opt/project-workbench/current
install -m 644 "$release/deploy/project-workbench.service" /etc/systemd/system/
install -m 644 "$release/deploy/project-workbench-channels.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now project-workbench.service
echo 'Application installed. Verify health and restart before switching Caddy or channel consumers.'
