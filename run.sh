#!/usr/bin/env bash
set -e

HOST="${1:-127.0.0.1}"
PORT="${2:-5432}"
HTTP_PORT="${3:-8080}"
MONITOR_PORT="${4:-5433}"
DATA_DIR="./data"
CONFIG_PATH="./vaultdb.yaml"

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
