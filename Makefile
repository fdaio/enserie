VERSION ?= 0.0.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)
HASH := sha256sum
ifeq ($(shell uname -s),Darwin)
HASH := shasum -a 256
endif
DPKG_DEB := $(shell command -v dpkg-deb 2>/dev/null)

.PHONY: build test vet fmt dist dist-bins dist-archives dist-deb dist-checksums

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/enserie ./cmd/enserie
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/enserie-relay ./cmd/enserie-relay

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Client: linux .deb and darwin tar.gz. Relay is packed separately.
dist: dist-checksums
	ls -lh dist/*.tar.gz dist/SHA256SUMS; ls -lh dist/*.deb 2>/dev/null || true

dist-bins: \
	dist/linux-amd64/enserie \
	dist/linux-arm64/enserie \
	dist/darwin-amd64/enserie \
	dist/darwin-arm64/enserie \
	dist/linux-amd64/enserie-relay \
	dist/linux-arm64/enserie-relay \
	dist/darwin-amd64/enserie-relay \
	dist/darwin-arm64/enserie-relay

dist/linux-amd64/enserie:
	mkdir -p dist/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie
dist/linux-amd64/enserie-relay:
	mkdir -p dist/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist/linux-arm64/enserie:
	mkdir -p dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie
dist/linux-arm64/enserie-relay:
	mkdir -p dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist/darwin-amd64/enserie:
	mkdir -p dist/darwin-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie
dist/darwin-amd64/enserie-relay:
	mkdir -p dist/darwin-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist/darwin-arm64/enserie:
	mkdir -p dist/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie
dist/darwin-arm64/enserie-relay:
	mkdir -p dist/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist-archives: dist-bins
	tar -C dist/darwin-amd64 -czf dist/enserie-darwin-amd64.tar.gz enserie
	tar -C dist/darwin-arm64 -czf dist/enserie-darwin-arm64.tar.gz enserie
	tar -C dist/linux-amd64 -czf dist/enserie-relay-linux-amd64.tar.gz enserie-relay
	tar -C dist/linux-arm64 -czf dist/enserie-relay-linux-arm64.tar.gz enserie-relay
	tar -C dist/darwin-amd64 -czf dist/enserie-relay-darwin-amd64.tar.gz enserie-relay
	tar -C dist/darwin-arm64 -czf dist/enserie-relay-darwin-arm64.tar.gz enserie-relay

dist-deb: dist-bins
ifeq ($(DPKG_DEB),)
	@echo "skip .deb: dpkg-deb not found"
else
	./scripts/pack-deb.sh "$(VERSION)" amd64 dist/linux-amd64/enserie dist/enserie_$(VERSION)_amd64.deb enserie
	./scripts/pack-deb.sh "$(VERSION)" arm64 dist/linux-arm64/enserie dist/enserie_$(VERSION)_arm64.deb enserie
	./scripts/pack-deb.sh "$(VERSION)" amd64 dist/linux-amd64/enserie-relay dist/enserie-relay_$(VERSION)_amd64.deb enserie-relay "Dedicated enserie WebSocket relay"
	./scripts/pack-deb.sh "$(VERSION)" arm64 dist/linux-arm64/enserie-relay dist/enserie-relay_$(VERSION)_arm64.deb enserie-relay "Dedicated enserie WebSocket relay"
endif

dist-checksums: dist-archives dist-deb
	cd dist && $(HASH) $$(ls *.tar.gz *.deb 2>/dev/null) > SHA256SUMS
