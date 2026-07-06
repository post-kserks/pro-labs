#!/usr/bin/env bash
set -euo pipefail

# TLS security scan using testssl.sh
# Usage: ./tools/security/tls_scan.sh [host:port]
#
# Prerequisites:
#   - Docker must be running
#   - VaultDB server must be accessible at the specified address

HOST="${1:-127.0.0.1:5433}"

echo "=== VaultDB TLS Security Scan ==="
echo "Target: ${HOST}"
echo ""

# Check if testssl.sh is available
if ! command -v testssl &>/dev/null; then
    echo "testssl.sh not found locally, using Docker..."
    docker run --rm -it drwetter/testssl.sh \
        --severity HIGH \
        --protocols \
        --vulnerable \
        --quiet \
        "${HOST}"
else
    testssl \
        --severity HIGH \
        --protocols \
        --vulnerable \
        --quiet \
        "${HOST}"
fi

echo ""
echo "=== Scan Complete ==="
echo ""
echo "Required pass criteria:"
echo "  - TLS 1.0/1.1 disabled"
echo "  - No weak cipher suites"
echo "  - No Heartbleed/POODLE/BEAST vulnerabilities"
echo "  - No CRITICAL or HIGH severity findings"
