VERSION ?= 0.0.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)
UNAME_S := $(shell uname -s)
HASH := sha256sum
ifeq ($(UNAME_S),Darwin)
HASH := shasum -a 256
endif
DPKG_DEB := $(shell command -v dpkg-deb 2>/dev/null)
RPMBUILD := $(shell command -v rpmbuild 2>/dev/null)
PKGBUILD := $(shell command -v pkgbuild 2>/dev/null)
# Which architecture this host builds rpms for. Distro rpm builds for the host
# architecture only, so the release workflow sets this per runner. uname -m
# answers x86_64 where the Go spelling is amd64, and both dist/ paths and the
# packer scripts use the Go spelling.
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),x86_64)
ARCH ?= amd64
else ifeq ($(UNAME_M),aarch64)
ARCH ?= arm64
else
ARCH ?= $(UNAME_M)
endif

.PHONY: build test vet fmt dist dist-linux dist-darwin \
	dist-linux-bins dist-darwin-bins dist-linux-archives dist-darwin-archives \
	dist-deb dist-rpm dist-pkg dist-homebrew \
	dist-linux-checksums dist-darwin-checksums

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ens ./cmd/ens
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/enserie-relay ./cmd/enserie-relay

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Linux-cross darwin binaries are unsigned and fail Gatekeeper (SIGKILL).
# Build each OS on that OS.
ifeq ($(UNAME_S),Darwin)
dist: dist-darwin
else
dist: dist-linux
endif

dist-linux: dist-linux-checksums
	ls -lh dist/*.deb dist/ens-linux-*.tar.gz dist/enserie-relay-linux-*.tar.gz dist/SHA256SUMS

dist-darwin: dist-darwin-checksums
	ls -lh dist/ens-darwin-*.tar.gz dist/enserie-relay-darwin-*.tar.gz dist/ens_*.pkg dist/ens.rb dist/SHA256SUMS

dist-linux-bins: \
	dist/linux-amd64/ens \
	dist/linux-arm64/ens \
	dist/linux-amd64/enserie-relay \
	dist/linux-arm64/enserie-relay

dist-darwin-bins: \
	dist/darwin-amd64/ens \
	dist/darwin-arm64/ens \
	dist/darwin-amd64/enserie-relay \
	dist/darwin-arm64/enserie-relay

dist/linux-amd64/ens:
	mkdir -p dist/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/ens
dist/linux-amd64/enserie-relay:
	mkdir -p dist/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist/linux-arm64/ens:
	mkdir -p dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/ens
dist/linux-arm64/enserie-relay:
	mkdir -p dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay

dist/darwin-amd64/ens:
	mkdir -p dist/darwin-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/ens
	./scripts/codesign-darwin.sh $@
dist/darwin-amd64/enserie-relay:
	mkdir -p dist/darwin-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay
	./scripts/codesign-darwin.sh $@

dist/darwin-arm64/ens:
	mkdir -p dist/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/ens
	./scripts/codesign-darwin.sh $@
dist/darwin-arm64/enserie-relay:
	mkdir -p dist/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/enserie-relay
	./scripts/codesign-darwin.sh $@

dist-linux-archives: dist-linux-bins
	tar -C dist/linux-amd64 -czf dist/ens-linux-amd64.tar.gz ens
	tar -C dist/linux-arm64 -czf dist/ens-linux-arm64.tar.gz ens
	tar -C dist/linux-amd64 -czf dist/enserie-relay-linux-amd64.tar.gz enserie-relay
	tar -C dist/linux-arm64 -czf dist/enserie-relay-linux-arm64.tar.gz enserie-relay

dist-darwin-archives: dist-darwin-bins
	tar -C dist/darwin-amd64 -czf dist/ens-darwin-amd64.tar.gz ens
	tar -C dist/darwin-arm64 -czf dist/ens-darwin-arm64.tar.gz ens
	tar -C dist/darwin-amd64 -czf dist/enserie-relay-darwin-amd64.tar.gz enserie-relay
	tar -C dist/darwin-arm64 -czf dist/enserie-relay-darwin-arm64.tar.gz enserie-relay

dist-deb: dist-linux-bins
ifeq ($(DPKG_DEB),)
	@echo "skip .deb: dpkg-deb not found"
else
	./scripts/pack-deb.sh "$(VERSION)" amd64 dist/linux-amd64/ens dist/ens_$(VERSION)_amd64.deb ens
	./scripts/pack-deb.sh "$(VERSION)" arm64 dist/linux-arm64/ens dist/ens_$(VERSION)_arm64.deb ens
	./scripts/pack-deb.sh "$(VERSION)" amd64 dist/linux-amd64/enserie-relay dist/enserie-relay_$(VERSION)_amd64.deb enserie-relay "Dedicated enserie WebSocket relay"
	./scripts/pack-deb.sh "$(VERSION)" arm64 dist/linux-arm64/enserie-relay dist/enserie-relay_$(VERSION)_arm64.deb enserie-relay "Dedicated enserie WebSocket relay"
endif

# Distro rpm knows only the host architecture, so this packs one architecture
# per run. Call it with ARCH=amd64 on an x86_64 host or ARCH=arm64 on an aarch64
# one. Set RPMBUILD to skip it where rpmbuild is absent.
dist-rpm: dist-linux-bins
ifeq ($(RPMBUILD),)
	@echo "skip .rpm: rpmbuild not found"
else
	./scripts/pack-rpm.sh "$(VERSION)" "$(ARCH)" dist/linux-$(ARCH)/ens dist/ens_$(VERSION)_$(ARCH).rpm ens
endif

# Installs /usr/local/bin/ens, for people who would rather double-click.
dist-pkg: dist-darwin-bins
ifeq ($(PKGBUILD),)
	@echo "skip .pkg: pkgbuild not found"
else
	./scripts/pack-pkg.sh "$(VERSION)" arm64 dist/darwin-arm64/ens dist/ens_$(VERSION)_arm64.pkg
	./scripts/pack-pkg.sh "$(VERSION)" amd64 dist/darwin-amd64/ens dist/ens_$(VERSION)_amd64.pkg
endif

# A formula is only correct once the tarball it names exists, so generate it
# from the release tarball rather than keeping a checked-in copy to update.
dist-homebrew: dist-darwin-archives
	./scripts/homebrew-formula.sh "$(VERSION)" arm64 dist/ens-darwin-arm64.tar.gz dist/ens.rb

dist-linux-checksums: dist-linux-archives dist-deb
	cd dist && $(HASH) $$(ls *.deb ens-linux-*.tar.gz enserie-relay-linux-*.tar.gz 2>/dev/null) > SHA256SUMS

dist-darwin-checksums: dist-darwin-archives dist-pkg dist-homebrew
	cd dist && $(HASH) $$(ls ens-darwin-*.tar.gz enserie-relay-darwin-*.tar.gz ens_*.pkg ens.rb 2>/dev/null) > SHA256SUMS
