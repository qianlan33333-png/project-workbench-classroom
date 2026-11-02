#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
if (($#)); then
 echo 'Historical increment archive entry retired; use install-release.sh or a prepared candidate.' >&2
 exit 2
fi
exec bash "$here/deploy-today.sh" "${PW_RELEASE:?Prepare a fresh candidate and set PW_RELEASE}"
