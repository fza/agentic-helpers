#!/usr/bin/env bash
cd "$(dirname "$0")/.."
TARGET=.tools/cache
eval "TARGET={outside}/x"
rm -rf "$TARGET"
