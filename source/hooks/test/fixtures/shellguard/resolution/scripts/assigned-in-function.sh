#!/usr/bin/env bash
cd "$(dirname "$0")/.."
OPENGREP_TAG=v1.33.1
locate() {
	OPENGREP_HOME=.tools/opengrep/$OPENGREP_TAG
}
locate
rm -f "$OPENGREP_HOME/opengrep.download"
