#!/bin/bash
set -euo pipefail
here=$(cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$here/release-common.py" cleanup --legacy
