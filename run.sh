#!/usr/bin/env bash
set -e

HOST="${1:-127.0.0.1}"
PORT="${2:-5432}"
HTTP_PORT="${3:-8080}"
MONITOR_PORT="${4:-5433}"
DATA_DIR="./data"
CONFIG_PATH="./vaultdb.yaml"

validate_port() {
    local port="$1"
    local name="$2"
    if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
        echo "[ERROR] Invalid $name: $port (must be 1-65535)"
        exit 1
    fi
}

validate_port "$PORT" "port"
validate_port "$HTTP_PORT" "http-port"
validate_port "$MONITOR_PORT" "monitor-port"

echo "╔════════════════════════════════════════╗"
echo "║          VaultDB  is starting          ║"
echo "╚════════════════════════════════════════╝"

if [ ! -f "./build/vaultdb-server" ]; then
    echo "[!] Server not found. Running build.sh first..."
    bash build.sh
fi

mkdir -p "$DATA_DIR"

echo "[*] Starting VaultDB server on $HOST:$PORT"
echo "[*] HTTP API/Web UI: $HOST:$HTTP_PORT"
echo "[*] Monitor API: $HOST:$MONITOR_PORT"
echo "[*] Data directory: $DATA_DIR"
echo ""

exec ./build/vaultdb-server \
    -host "$HOST" \
    -port "$PORT" \
    -http-port "$HTTP_PORT" \
    -monitor-port "$MONITOR_PORT" \
    -data "$DATA_DIR" \
    -config "$CONFIG_PATH"
