#!/usr/bin/env bash
cd "$(dirname "$0")/.."
OPENGREP_TAG=v1.33.1
mkdir -p .tools && OPENGREP_HOME=.tools/opengrep/$OPENGREP_TAG
rm -f "$OPENGREP_HOME/opengrep.download"
