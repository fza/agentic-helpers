#!/usr/bin/env bash
cd "$(dirname "$0")/.."
OPENGREP_TAG=v1.33.1
if [ -d .tools ]; then
	OPENGREP_HOME=.tools/opengrep/$OPENGREP_TAG
fi
rm -f "$OPENGREP_HOME/opengrep.download"
