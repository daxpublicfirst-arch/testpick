# testpick — Makefile

BINARY := testpick
VERSION := 0.1.0
GO := go
GOFLAGS := -trimpath
LDFLAGS := -s -w

# Static builds: CGO off, no dynamic linker.
BUILD := CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o

.PHONY: build test vet fmt self-test doctor release clean install

build:
	$(BUILD) bin/$(BINARY) ./cmd/testpick

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l .

self-test: build
	./bin/$(BINARY) self-test

doctor: build
	./bin/$(BINARY) doctor

# Release: static binaries for linux-amd64 and linux-arm64, a tarball per
# arch, and a SHA256SUMS file. Binaries are static (CGO_ENABLED=0).
release: clean
	mkdir -p dist
	GOOS=linux GOARCH=amd64 $(BUILD) dist/$(BINARY)-linux-amd64 ./cmd/testpick
	GOOS=linux GOARCH=arm64 $(BUILD) dist/$(BINARY)-linux-arm64 ./cmd/testpick
	cp plugin.toml README.md LICENSE CHANGELOG.md dist/
	tar -czf dist/$(BINARY)-$(VERSION)-linux-amd64.tar.gz -C dist $(BINARY)-linux-amd64 plugin.toml README.md LICENSE CHANGELOG.md
	tar -czf dist/$(BINARY)-$(VERSION)-linux-arm64.tar.gz -C dist $(BINARY)-linux-arm64 plugin.toml README.md LICENSE CHANGELOG.md
	cd dist && sha256sum $(BINARY)-$(VERSION)-linux-amd64.tar.gz $(BINARY)-$(VERSION)-linux-arm64.tar.gz $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 > SHA256SUMS.txt
	cp dist/SHA256SUMS.txt SHA256SUMS.txt

install: build
	install -m 0755 bin/$(BINARY) $(or $(DESTDIR),/usr/local/bin)/$(BINARY)

clean:
	rm -rf bin dist SHA256SUMS.txt
