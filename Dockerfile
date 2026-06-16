FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY server/go.mod ./
RUN go mod download

COPY server/ .

ARG VERSION=dev
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o vaultdb-server \
    ./cmd/vaultdb-server

RUN addgroup -S vaultdb && adduser -S vaultdb -G vaultdb

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /build/vaultdb-server /vaultdb-server

COPY vaultdb.yaml.example /etc/vaultdb/vaultdb.yaml

VOLUME ["/data"]

EXPOSE 5432
EXPOSE 8080
EXPOSE 5433

HEALTHCHECK --interval=10s --timeout=5s --retries=3 --start-period=5s \
  CMD ["/vaultdb-server", "--health-check", "--monitor-port", "5433"]

USER vaultdb
ENTRYPOINT ["/vaultdb-server"]
CMD ["--host", "0.0.0.0", "--port", "5432", "--http-port", "8080", "--monitor-port", "5433", "--data", "/data", "--config", "/etc/vaultdb/vaultdb.yaml"]
