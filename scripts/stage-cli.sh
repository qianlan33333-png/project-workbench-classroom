#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p web/dist/cli
cp cli/pwb.py web/dist/cli/pwb.py
cp cli/README.md web/dist/cli/README.md
