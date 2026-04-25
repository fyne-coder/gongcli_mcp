.PHONY: build test fmt clean

build:
	go build -o bin/gongctl ./cmd/gongctl
	go build -o bin/gongmcp ./cmd/gongmcp

test:
	go test ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin dist coverage.out
