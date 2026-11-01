#!/usr/bin/env bash
cd "$(dirname "$0")/.."
TARGET=.tools/cache
clean() {
	rm -rf "$TARGET"
}
TARGET={outside}/x
clean
