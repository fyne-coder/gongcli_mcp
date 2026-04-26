.PHONY: build test fmt docker-build docker-smoke clean

build:
	go build -o bin/gongctl ./cmd/gongctl
	go build -o bin/gongmcp ./cmd/gongmcp

test:
	go test ./...

fmt:
	gofmt -w cmd internal

docker-build:
	docker build -t gongctl:local .

docker-smoke: docker-build
	./scripts/docker-smoke.sh

clean:
	rm -rf bin dist coverage.out
