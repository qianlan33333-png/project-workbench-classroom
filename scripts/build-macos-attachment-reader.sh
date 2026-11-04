#!/bin/sh
# Local development only; production uses the Linux sandbox's PDF/OCR packages.
set -eu
cd "$(dirname "$0")/.."
test "$(uname -s)" = Darwin
mkdir -p runtime/.local
/usr/bin/swiftc -O runtime/attachment-reader-macos.swift -o runtime/.local/attachment-reader-macos
