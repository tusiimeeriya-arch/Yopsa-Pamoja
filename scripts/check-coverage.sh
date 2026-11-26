#!/bin/sh
# Enforce the coverage gate for the financial and security-critical packages.
#
# Usage: scripts/check-coverage.sh [go test -coverprofile file]
#   COVERAGE_THRESHOLD  minimum percentage per package (default 85)
#
# A package that does not exist yet is reported as pending, not as passing:
# the gate tightens as each phase lands, and the report never claims coverage
# for code that is not written.

set -eu

THRESHOLD="${COVERAGE_THRESHOLD:-85}"
PROFILE="${1:-/tmp/coverage.out}"

cd "$(dirname "$0")/../backend"

status=0
for pkg in payments loans rbac audit attendance; do
    dir="./internal/${pkg}"
    if [ ! -d "$dir" ]; then
        echo "pending: ${dir} not delivered yet"
        continue
    fi
    if [ ! -f "$PROFILE" ]; then
        echo "error: coverage profile ${PROFILE} not found" >&2
        exit 1
    fi
    # Aggregate statement coverage for this package from the merged profile.
    pct=$(awk -v prefix="github.com/yopsa/pamoja/backend/internal/${pkg}/" '
        BEGIN { total = 0; covered = 0 }
        $0 ~ prefix {
            # lines look like: file.go:12.3,15.9 3 1
            n = $2; c = $3
            total += n
            if (c > 0) covered += n
        }
        END {
            if (total == 0) { print ""; exit }
            printf "%.1f", (covered * 100) / total
        }' "$PROFILE")

    if [ -z "$pct" ]; then
        echo "pending: ${dir} has no covered statements yet"
        continue
    fi
    echo "${pkg}: ${pct}% (threshold ${THRESHOLD}%)"
    if ! awk -v p="$pct" -v t="$THRESHOLD" 'BEGIN { exit (p + 0 >= t + 0) ? 0 : 1 }'; then
        echo "FAIL: ${pkg} coverage ${pct}% is below ${THRESHOLD}%" >&2
        status=1
    fi
done

exit "$status"
