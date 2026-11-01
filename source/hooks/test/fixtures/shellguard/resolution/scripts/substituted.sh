#!/usr/bin/env bash
cd "$(dirname "$0")/.."
TARGET=$(pwd)/.tools/cache
rm -rf "$TARGET"
