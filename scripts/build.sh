#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
: "${GO:=go}"
: "${PNPM:=pnpm}"
"$GO" test -race ./...
"$GO" vet ./...
"$PNPM" --dir web install --frozen-lockfile
"$PNPM" --dir web run build
./scripts/stage-cli.sh
"$PNPM" --dir runtime install --frozen-lockfile
"$PNPM" --dir runtime run check
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build -trimpath -o bin/workbench-linux-amd64 ./cmd/workbench
