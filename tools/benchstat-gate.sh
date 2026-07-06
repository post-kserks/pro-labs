#!/usr/bin/env bash
set -euo pipefail

# benchstat-gate.sh — compare two benchmark result files and fail if any
# benchmark regresses by more than 10%.
#
# Usage: benchstat-gate.sh old.txt new.txt [threshold_pct]
# Exit 0 if OK, exit 1 if regression found.

THRESHOLD="${3:-10}"

if [ $# -lt 2 ]; then
  echo "Usage: $0 old.txt new.txt [threshold_pct]"
  echo "  threshold_pct: regression percentage to trigger failure (default: 10)"
  exit 2
fi

OLD="$1"
NEW="$2"

if [ ! -f "$OLD" ]; then
  echo "ERROR: Old benchmark file not found: $OLD"
  exit 2
fi

if [ ! -f "$NEW" ]; then
  echo "ERROR: New benchmark file not found: $NEW"
  exit 2
fi

echo "=== Benchstat Comparison ==="
echo "Old: $OLD"
echo "New: $NEW"
echo "Regression threshold: ${THRESHOLD}%"
echo ""

# Ensure GOPATH/bin is on PATH so benchstat can be found after install
export PATH="$PATH:$(go env GOPATH)/bin"

# Run benchstat (install if missing)
if ! command -v benchstat &>/dev/null; then
  echo "Installing benchstat..."
  go install golang.org/x/perf/cmd/benchstat@latest
  if ! command -v benchstat &>/dev/null; then
    echo "ERROR: Could not install benchstat"
    exit 2
  fi
fi

OUTPUT=$(benchstat "$OLD" "$NEW" 2>&1) || true
echo "$OUTPUT"
echo ""

# Parse benchstat output for regressions exceeding the threshold.
# benchstat marks regressions with "~" or shows negative delta%.
# Lines look like:  BenchmarkFoo-8   100ns ± 5%   120ns ± 3%  +20.00%  (p=0.01 n=5+5)
# We look for "+XX.XX%" at the end of data lines.

regression_found=0
while IFS= read -r line; do
  # Match lines with a percentage delta like "+20.00%" or "+20%"
  if echo "$line" | grep -qE '\+[0-9]+\.[0-9]+%'; then
    pct=$(echo "$line" | grep -oE '\+[0-9]+\.[0-9]+%' | head -1 | tr -d '+%')
    # Compare using bc if available, else awk
    if command -v bc &>/dev/null; then
      exceeded=$(echo "$pct > $THRESHOLD" | bc -l)
    else
      exceeded=$(awk "BEGIN { print ($pct > $THRESHOLD) ? 1 : 0 }")
    fi
    if [ "$exceeded" = "1" ]; then
      bench_name=$(echo "$line" | awk '{print $1}')
      echo "REGRESSION: ${bench_name} regressed by ${pct}% (threshold: ${THRESHOLD}%)"
      regression_found=1
    fi
  fi
done <<< "$OUTPUT"

echo ""
if [ "$regression_found" = "1" ]; then
  echo "FAIL: Benchmark regression detected exceeding ${THRESHOLD}%"
  exit 1
else
  echo "PASS: No regressions exceeding ${THRESHOLD}%"
  exit 0
fi
