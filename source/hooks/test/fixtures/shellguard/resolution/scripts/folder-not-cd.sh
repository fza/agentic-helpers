#!/bin/sh
cd "$(pushd "$(dirname "$0")" && pwd)/.."
rm -rf .tools/cache
