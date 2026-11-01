#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
OPENGREP_TAG=v1.33.1
OPENGREP_HOME=.tools/opengrep/$OPENGREP_TAG
mkdir -p "$OPENGREP_HOME"
rm -f "$OPENGREP_HOME/opengrep.download"
curl -fsSL -o "$OPENGREP_HOME/opengrep.download" "https://example.invalid/opengrep/$OPENGREP_TAG"
mv "$OPENGREP_HOME/opengrep.download" "$OPENGREP_HOME/opengrep"
