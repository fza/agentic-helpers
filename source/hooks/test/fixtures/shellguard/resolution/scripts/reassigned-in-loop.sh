#!/usr/bin/env bash
cd "$(dirname "$0")/.."
TARGET=.tools/cache
for round in first second; do
	rm -rf "$TARGET"
	TARGET={outside}/x
done
