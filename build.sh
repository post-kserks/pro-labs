#!/usr/bin/env bash
set -e

log() {
    echo "$1"
}

echo "╔════════════════════════════════╗"
echo "║  VaultDB Build System v2.0     ║"
echo "╚════════════════════════════════╝"

OS="$(uname -s)"
log "[*] Detected OS: $OS"

install_deps() {
    case "$OS" in
        Linux)
            if command -v apt-get >/dev/null 2>&1; then
                log "[*] Using apt-get"
                sudo apt-get update -qq
                sudo apt-get install -y -qq cmake g++ golang-go
            elif command -v pacman >/dev/null 2>&1; then
                log "[*] Using pacman"
                sudo pacman -Sy --noconfirm cmake gcc go
            elif command -v dnf >/dev/null 2>&1; then
                log "[*] Using dnf"
                sudo dnf install -y cmake gcc-c++ golang
            else
                log "[!] Unknown package manager. Install cmake, g++, go manually."
                exit 1
            fi
            ;;
        Darwin)
            if command -v brew >/dev/null 2>&1; then
                log "[*] Using brew"
                brew install cmake go
            else
                log "[!] Homebrew not found. Install it from https://brew.sh"
                exit 1
            fi
            ;;
        *)
            log "[!] Unsupported OS: $OS"
            exit 1
            ;;
    esac
}

build_webui() {
    WEB_DIR="server/internal/httpserver/web"
    DIST_DIR="${WEB_DIR}/dist"

    if ! command -v node >/dev/null 2>&1; then
        log "[SKIP] Node.js not found. Web UI will use a fallback page."
        mkdir -p "$DIST_DIR"
        cat > "${DIST_DIR}/index.html" <<'EOF'
<!doctype html><html><body><p>Web UI not built. Install Node.js and run build.sh again.</p></body></html>
EOF
        return 0
    fi

    if [ ! -f "${WEB_DIR}/package.json" ]; then
        log "[SKIP] ${WEB_DIR}/package.json not found. Keeping existing dist/."
        return 0
    fi

    NODE_VERSION="$(node --version)"
    log "[*] Building Web UI (Node.js ${NODE_VERSION})..."
    (
        cd "$WEB_DIR"
        npm install --silent
        npm run build
    )
    log "[OK] Web UI built: ${DIST_DIR}"
}

build_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        log "[SKIP] Docker not found. Skipping image build."
        log "       Install Docker from https://docs.docker.com/get-docker/"
        return 0
    fi

    log "[*] Building Docker image vaultdb/vaultdb:1.2.0..."
    docker build -t vaultdb/vaultdb:1.2.0 -t vaultdb/vaultdb:latest .

    IMAGE_SIZE="$(docker image inspect vaultdb/vaultdb:1.2.0 --format='{{.Size}}' 2>/dev/null || echo '?')"
    log "[OK] Docker image built."
    log "     Tag:  vaultdb/vaultdb:1.2.0"
    log "     Size: ${IMAGE_SIZE} bytes"
    log ""
    log "     Quick start:"
    log "     docker run -p 5432:5432 -p 8080:8080 vaultdb/vaultdb:1.2.0"
    log ""
    log "     With persistence:"
    log "     docker compose up -d"
}

log "[*] Checking dependencies..."
MISSING=0
command -v go >/dev/null 2>&1 || MISSING=1
command -v cmake >/dev/null 2>&1 || MISSING=1
command -v g++ >/dev/null 2>&1 || command -v c++ >/dev/null 2>&1 || MISSING=1

if [ "$MISSING" -eq 1 ]; then
    log "[*] Installing missing dependencies..."
    install_deps
fi
log "[OK] All dependencies present."

mkdir -p build

build_webui

log "[*] Building VaultDB server (Go)..."
cd server
GOCACHE="${GOCACHE:-/tmp/go-cache}" GOMODCACHE="${GOMODCACHE:-/tmp/go-mod-cache}" go build -o ../build/vaultdb-server ./cmd/vaultdb-server
cd ..
log "[OK] Server built: build/vaultdb-server"

log "[*] Building VaultDB benchmark tool..."
GOCACHE="${GOCACHE:-/tmp/go-cache}" GOMODCACHE="${GOMODCACHE:-/tmp/go-mod-cache}" go build -o build/benchmark tools/benchmark/main.go
log "[OK] Benchmark tool built: build/benchmark"

log "[*] Building VaultDB client (C++)..."
cmake -S client -B client/build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=. -Wno-dev
cmake --build client/build -- -j"$(nproc 2>/dev/null || sysctl -n hw.logicalcpu 2>/dev/null || echo 4)"

mkdir -p client/build/output
cp client/build/libvaultdb* client/build/output/ 2>/dev/null || true
cp client/build/vaultdb-shell client/build/output/
cp client/build/tui/vaultdb-tui client/build/output/

cp client/build/output/libvaultdb* build/ 2>/dev/null || true
cp client/build/output/vaultdb-shell build/
cp client/build/output/vaultdb-tui build/
log "[OK] Client built: build/libvaultdb*, build/vaultdb-shell, build/vaultdb-tui"

build_docker

echo ""
echo "╔════════════════════════════════╗"
echo "║  [BUILD COMPLETE]              ║"
echo "╚════════════════════════════════╝"
