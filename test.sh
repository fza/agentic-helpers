#!/usr/bin/env bash
#
# Every suite this repository carries. Each Python hook is driven as a
# subprocess, the way Claude Code drives it, so a suite proves the file that
# ships rather than an import of it. The Go hooks run their own module's tests.
#
# A suite's own status decides the run. Piping one into `tail` would hand the
# pipeline `tail`'s status instead, and every failure would read as a pass.

set -uo pipefail

cd "$(dirname "$0")"

failed=0
for suite in hooks/test_*.py; do
    printf '\n== %s\n' "$suite"
    if output=$(python3 "$suite" 2>&1); then
        printf '%s\n' "$output" | tail -3
    else
        printf '%s\n' "$output"
        failed=1
    fi
done

printf '\n== source/hooks\n'
if output=$(cd source/hooks && go vet ./... && go test -race -count=1 ./... 2>&1); then
    printf '%s\n' "$output" | tail -5
else
    printf '%s\n' "$output"
    failed=1
fi

exit "$failed"
