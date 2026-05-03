#!/usr/bin/env bash
set -e

echo "╔════════════════════════════════╗"
echo "║  VaultDB Build System v1.0     ║"
echo "╚════════════════════════════════╝"

OS="$(uname -s)"
echo "[*] Detected OS: $OS"

install_deps() {
    case "$OS" in
        Linux)
            if command -v apt-get >/dev/null 2>&1; then
                echo "[*] Using apt-get"
                sudo apt-get update -qq
                sudo apt-get install -y -qq cmake g++ golang-go
            elif command -v pacman >/dev/null 2>&1; then
                echo "[*] Using pacman"
                sudo pacman -Sy --noconfirm cmake gcc go
            elif command -v dnf >/dev/null 2>&1; then
                echo "[*] Using dnf"
                sudo dnf install -y cmake gcc-c++ golang
            else
                echo "[!] Unknown package manager. Install cmake, g++, go manually."
                exit 1
            fi
            ;;
        Darwin)
            if command -v brew >/dev/null 2>&1; then
                echo "[*] Using brew"
                brew install cmake go
            else
                echo "[!] Homebrew not found. Install it from https://brew.sh"
                exit 1
            fi
            ;;
        *)
            echo "[!] Unsupported OS: $OS"
            exit 1
            ;;
    esac
}

echo "[*] Checking dependencies..."

MISSING=0
command -v go >/dev/null 2>&1 || MISSING=1
command -v cmake >/dev/null 2>&1 || MISSING=1
command -v g++ >/dev/null 2>&1 || command -v c++ >/dev/null 2>&1 || MISSING=1

if [ "$MISSING" -eq 1 ]; then
    echo "[*] Installing missing dependencies..."
    install_deps
fi

echo "[OK] All dependencies present."

mkdir -p build

echo "[*] Building VaultDB server (Go)..."
cd server
GOCACHE="${GOCACHE:-/tmp/go-cache}" GOMODCACHE="${GOMODCACHE:-/tmp/go-mod-cache}" go build -o ../build/vaultdb-server ./cmd/vaultdb-server
cd ..
echo "[OK] Server built: build/vaultdb-server"

echo "[*] Building VaultDB client (C++)..."
cmake -S client -B client/build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=. -Wno-dev
cmake --build client/build -- -j"$(nproc 2>/dev/null || sysctl -n hw.logicalcpu 2>/dev/null || echo 4)"

mkdir -p client/build/output
cp client/build/libvaultdb* client/build/output/ 2>/dev/null || true
cp client/build/vaultdb-shell client/build/output/
cp client/build/tui/vaultdb-tui client/build/output/

cp client/build/output/libvaultdb* build/ 2>/dev/null || true
cp client/build/output/vaultdb-shell build/
cp client/build/output/vaultdb-tui build/
echo "[OK] Client built: build/libvaultdb*, build/vaultdb-shell, build/vaultdb-tui"

echo ""
echo "╔════════════════════════════════╗"
echo "║  [BUILD COMPLETE]  ⚔ ⚔ ⚔       ║"
echo "╚════════════════════════════════╝"
