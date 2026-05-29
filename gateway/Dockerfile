# ─── Stage 1: Builder ────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /build

# No external dependencies: copy go.mod and the source, then build.
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=1.0.0" \
    -o medvault-gateway \
    ./cmd/gateway

# ─── Stage 2: Final ──────────────────────────────────────────────
FROM scratch

COPY --from=builder /build/medvault-gateway /medvault-gateway

EXPOSE 4000

ENTRYPOINT ["/medvault-gateway"]
