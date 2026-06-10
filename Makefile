.PHONY: build docker-build docker-run docker-stop docker-clean docker-logs docker-health

IMAGE   := vaultdb/vaultdb
VERSION := $(shell cat VERSION)
TAG     := $(IMAGE):$(VERSION)
LDFLAGS := -X main.version=$(VERSION) -X main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build:
	cd server && go build -ldflags="$(LDFLAGS)" -o ../build/vaultdb-server ./cmd/vaultdb-server

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(TAG) \
		-t $(IMAGE):latest \
		.
	@echo "Built: $(TAG)"
	@docker image inspect $(TAG) --format='Size: {{.Size}} bytes'

docker-run:
	docker compose up -d
	@echo "VaultDB started. Web UI: http://localhost:8080"

docker-stop:
	docker compose down

docker-clean:
	docker compose down -v
	@echo "All data volumes removed."

docker-logs:
	docker compose logs -f vaultdb

docker-health:
	docker inspect --format='{{.State.Health.Status}}' vaultdb
