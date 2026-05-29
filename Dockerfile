FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY server/go.mod ./
RUN go mod download && go mod verify

COPY server/ .

ARG VERSION=1.0.2
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o vaultdb-server \
    ./cmd/vaultdb-server

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/vaultdb-server /vaultdb-server

VOLUME ["/data"]

EXPOSE 5432
EXPOSE 8080
EXPOSE 5433

ENTRYPOINT ["/vaultdb-server"]
CMD ["--host", "0.0.0.0", "--port", "5432", "--http-port", "8080", "--monitor-port", "5433", "--data", "/data", "--config", "/etc/vaultdb/vaultdb.yaml"]
