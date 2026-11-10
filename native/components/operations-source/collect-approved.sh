#!/bin/sh
set -eu

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
