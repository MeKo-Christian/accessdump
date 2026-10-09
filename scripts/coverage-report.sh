#!/usr/bin/env bash
# Turns a Go coverage profile into code-coverage-results.md: the total, then a
# per-package table. CI appends the file to the job summary and posts it as a
# sticky PR comment.
#
# Usage: scripts/coverage-report.sh [coverage.out] [code-coverage-results.md]
#
# The numbers come straight from the profile, the way `go test -cover` counts:
# covered statements over all statements. `go tool cover -func` is not used
# because it only sees named functions and misses closures in package-level
# variables, such as the RunE of every cobra command in cmd/.

set -euo pipefail

PROFILE="${1:-coverage.out}"
OUT="${2:-code-coverage-results.md}"

if [[ ! -f ${PROFILE} ]]; then
	echo "coverage profile ${PROFILE} not found" >&2
	exit 1
fi

MODULE="$(go list -m)"

# Profile lines: <import path>/<file>.go:<range> <statements> <count>. A block
# listed more than once counts as covered if any run hit it. The first output
# line is the total, the rest one row per package.
STATS="$(LC_ALL=C awk -v module="${MODULE}" '
	NR == 1 && $1 == "mode:" { next }
	{
		stmts[$1] = $2 + 0
		if (($3 + 0) > 0) hit[$1] = 1
	}
	END {
		for (block in stmts) {
			pkg = substr(block, 1, index(block, ":") - 1)
			sub(/\/[^\/]*$/, "", pkg)
			if (pkg == module) pkg = "."
			else sub("^" module "/", "", pkg)

			total[pkg] += stmts[block]
			all += stmts[block]
			if (block in hit) {
				covered[pkg] += stmts[block]
				allCovered += stmts[block]
			}
		}

		printf "%.1f%%\n", (all > 0 ? allCovered * 100 / all : 0)
		for (pkg in total) {
			printf "| %s | %.1f%% | %d |\n", pkg, covered[pkg] * 100 / total[pkg], total[pkg]
		}
	}' "${PROFILE}")"

TOTAL="$(head -n 1 <<<"${STATS}")"

{
	echo "## Coverage Summary"
	echo ""
	echo "### Total: ${TOTAL} of statements"
	echo ""
	echo "<details>"
	echo "<summary>Per-package breakdown (statement coverage)</summary>"
	echo ""
	echo "| Package | Coverage | Statements |"
	echo "|---------|----------|------------|"
	tail -n +2 <<<"${STATS}" | sort -t'|' -k4 -rn
	echo ""
	echo "</details>"
} >"${OUT}"

echo "${TOTAL}"
