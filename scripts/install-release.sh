#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$here/release-common.py" install "${1:?Pass RELEASE.tar.gz}" "${2:?Pass a fresh RELEASE_NAME}"
