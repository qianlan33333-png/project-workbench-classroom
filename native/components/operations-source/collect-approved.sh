#!/bin/sh
set -eu

if [ "$#" -eq 2 ] && [ "$1" = directory ] && [ "$2" = v1 ]; then
    exec /usr/bin/env -i /usr/bin/python3 -I /opt/workbench-operations-source/directory.py
fi
if [ "$#" -eq 2 ] && [ "$1" = package ] && [ "$2" = v1 ]; then
    exec /usr/bin/env -i /usr/bin/python3 -I /opt/workbench-operations-source/directory.py execute
fi
[ "$#" -eq 1 ] || exit 64
case $1 in
    ''|*[!a-zA-Z0-9_-]*) exit 64 ;;
esac
[ "${#1}" -le 96 ] || exit 64
umask 077
exec /usr/bin/env -i /usr/bin/python3 -I /opt/workbench-operations-source/collect.py \
    --refs /etc/workbench-operations-source/refs \
    --state-dir /var/lib/workbench-operations-source/cache \
    --operator-env /etc/daru-ops-console.env --operation-key="$1" --limit 50
