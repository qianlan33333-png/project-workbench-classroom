#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
task_commit=ce7073d51254c26cd63b22a60835b8f948deb447
: "${PNPM:=pnpm}"
if [ ! -d vendor-src/deepseek-harness/.git ]; then
 mkdir -p vendor-src
 git clone https://github.com/deepseek-ai/deepseek-harness.git vendor-src/deepseek-harness
fi
cd vendor-src/deepseek-harness
git checkout --detach "$task_commit"
test "$(git rev-parse HEAD)" = "$task_commit"
"$PNPM" install --frozen-lockfile
"$PNPM" run build:artifacts
"$PNPM" run build:check
"$PNPM" --filter @deepseek-ai/dsh deploy --prod --legacy --offline --frozen-lockfile ../../.var/harness-deploy-offline
cd ../..
python3 scripts/portable-harness.py
python3 scripts/prepare-linux-addon.py
node .var/harness-deploy-offline/lib/bin.js --version
