#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
if (($#)); then
 echo 'Rollback now uses the previous symlink; historical data snapshots are not restored.' >&2
 exit 2
fi
exec python3 "$here/release-common.py" rollback
