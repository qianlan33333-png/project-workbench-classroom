#!/bin/sh
set -eu

[ "${SSH_ORIGINAL_COMMAND-}" = package ] || exit 64
exec /usr/bin/sudo -n -- /opt/workbench-operations-source/collect-approved.sh package v1
