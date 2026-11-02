#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
exec bash "$here/deploy-today.sh" "${PW_RELEASE:?Historical fixed-version entry retired; prepare a fresh candidate and set PW_RELEASE}"
