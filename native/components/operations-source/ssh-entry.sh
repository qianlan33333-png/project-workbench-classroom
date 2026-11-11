#!/bin/sh
set -eu

case ${SSH_ORIGINAL_COMMAND-} in
    'read '*) operation=${SSH_ORIGINAL_COMMAND#read } ;;
    'directory') exec /usr/bin/sudo -n -- /opt/workbench-operations-source/collect-approved.sh directory v1 ;;
    *) exit 64 ;;
esac
case $operation in
    ''|*[!a-zA-Z0-9_-]*) exit 64 ;;
esac
[ "${#operation}" -le 96 ] || exit 64
exec /usr/bin/sudo -n -- /opt/workbench-operations-source/collect-approved.sh "$operation"
