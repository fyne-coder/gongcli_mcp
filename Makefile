VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/arthurlee/gongctl/internal/version.Version=$(VERSION) -X github.com/arthurlee/gongctl/internal/version.Commit=$(COMMIT) -X github.com/arthurlee/gongctl/internal/version.Date=$(DATE)

.PHONY: build test fmt docker-build docker-smoke clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gongctl ./cmd/gongctl
	go build -ldflags "$(LDFLAGS)" -o bin/gongmcp ./cmd/gongmcp

test:
	go test ./...

fmt:
	gofmt -w cmd internal

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t gongctl:local .

docker-smoke: docker-build
	./scripts/docker-smoke.sh

clean:
	rm -rf bin dist coverage.out
