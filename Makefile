.PHONY: build test race bench vet lint

build:
	go build -o sitewatch ./cmd/sitewatch

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -bench=. ./...

vet:
	go vet ./...

lint:
	golangci-lint run
