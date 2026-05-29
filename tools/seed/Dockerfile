FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o medvault-seed .

FROM scratch
COPY --from=builder /build/medvault-seed /medvault-seed
ENTRYPOINT ["/medvault-seed"]
