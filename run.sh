#!/usr/bin/env bash
set -e

HOST="${1:-127.0.0.1}"
PORT="${2:-5432}"
DATA_DIR="./data"

echo "╔════════════════════════════════════════╗"
echo "║          PixelDB  is starting          ║"
echo "╚════════════════════════════════════════╝"

if [ ! -f "./build/pixeldb-server" ]; then
    echo "[!] Server not found. Running build.sh first..."
    bash build.sh
fi

mkdir -p "$DATA_DIR"

echo "[*] Starting PixelDB server on $HOST:$PORT"
echo "[*] Data directory: $DATA_DIR"
echo ""

exec ./build/pixeldb-server -host "$HOST" -port "$PORT" -data "$DATA_DIR"
