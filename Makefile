.PHONY: build test vet fmt

build:
	mkdir -p bin
	go build -o bin/enserie ./cmd/enserie
	go build -o bin/enserie-relay ./cmd/enserie-relay

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
