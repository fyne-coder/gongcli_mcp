VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE_BASE ?= ghcr.io/fyne-coder/gongcli_mcp
LDFLAGS := -X github.com/fyne-coder/gongcli_mcp/internal/version.Version=$(VERSION) -X github.com/fyne-coder/gongcli_mcp/internal/version.Commit=$(COMMIT) -X github.com/fyne-coder/gongcli_mcp/internal/version.Date=$(DATE)

.PHONY: build test fmt vet secret-scan sbom checksums docker-build docker-build-mcp docker-build-ghcr docker-build-ghcr-mcp docker-smoke clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gongctl ./cmd/gongctl
	go build -ldflags "$(LDFLAGS)" -o bin/gongmcp ./cmd/gongmcp

test:
	go test ./...

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

secret-scan:
	./scripts/secret-scan.sh

sbom:
	./scripts/generate-sbom.sh

checksums: build
	mkdir -p dist
	shasum -a 256 bin/gongctl bin/gongmcp > dist/checksums.txt

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:local .

docker-build-mcp:
	docker build --target mcp --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:mcp-local .

docker-build-ghcr:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_BASE)/gongctl:v$(VERSION) -t $(IMAGE_BASE)/gongctl:$(VERSION) .

docker-build-ghcr-mcp:
	docker build --target mcp --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_BASE)/gongmcp:v$(VERSION) -t $(IMAGE_BASE)/gongmcp:$(VERSION) .

docker-smoke: docker-build docker-build-mcp
	./scripts/docker-smoke.sh

clean:
	rm -rf bin dist coverage.out
