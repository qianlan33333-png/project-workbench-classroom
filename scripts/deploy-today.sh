#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$here/release-common.py" deploy "${1:-${PW_RELEASE:?Set PW_RELEASE or pass a prepared release name}}"
