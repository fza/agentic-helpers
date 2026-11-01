#!/usr/bin/env bash
cd "$(dirname "$0")/.."
TARGET=.tools/cache
read -r TARGET
rm -rf "$TARGET"
