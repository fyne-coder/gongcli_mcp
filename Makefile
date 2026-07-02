VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE_BASE ?= ghcr.io/fyne-coder/gongcli_mcp
LDFLAGS := -X github.com/fyne-coder/gongcli_mcp/internal/version.Version=$(VERSION) -X github.com/fyne-coder/gongcli_mcp/internal/version.Commit=$(COMMIT) -X github.com/fyne-coder/gongcli_mcp/internal/version.Date=$(DATE)

.PHONY: build test fmt vet secret-scan public-surface-scan public-surface-scan-test release-public-surface-scan sbom checksums docker-build docker-build-mcp docker-build-mcp-gateway docker-build-ghcr docker-build-ghcr-mcp docker-build-ghcr-mcp-gateway docker-smoke postgres-backup-restore-smoke clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gongctl ./cmd/gongctl
	go build -ldflags "$(LDFLAGS)" -o bin/gongmcp ./cmd/gongmcp
	go build -ldflags "$(LDFLAGS)" -o bin/gongmcp-gateway ./cmd/gongmcp-gateway

test:
	go test ./...

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

secret-scan:
	./scripts/secret-scan.sh

public-surface-scan:
	./scripts/public-surface-scan.sh

public-surface-scan-test:
	./scripts/public-surface-scan-test.sh

release-public-surface-scan:
	./scripts/public-surface-scan.sh --release-bodies --repo fyne-coder/gongcli_mcp

sbom:
	./scripts/generate-sbom.sh

checksums: build
	mkdir -p dist
	shasum -a 256 bin/gongctl bin/gongmcp > dist/checksums.txt

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:local .

docker-build-mcp:
	docker build --target mcp --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:mcp-local .

docker-build-mcp-gateway:
	docker build --target mcp-gateway --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:mcp-gateway-local .

docker-build-ghcr:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_BASE)/gongctl:v$(VERSION) -t $(IMAGE_BASE)/gongctl:$(VERSION) .

docker-build-ghcr-mcp:
	docker build --target mcp --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_BASE)/gongmcp:v$(VERSION) -t $(IMAGE_BASE)/gongmcp:$(VERSION) .

docker-build-ghcr-mcp-gateway:
	docker build --target mcp-gateway --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_BASE)/gongmcp-gateway:v$(VERSION) -t $(IMAGE_BASE)/gongmcp-gateway:$(VERSION) .

docker-smoke: docker-build docker-build-mcp
	./scripts/docker-smoke.sh

postgres-backup-restore-smoke:
	./scripts/postgres-backup-restore-smoke.sh

clean:
	rm -rf bin dist coverage.out
