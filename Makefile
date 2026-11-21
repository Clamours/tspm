BINARY := tspm
PKG := ./cmd/tspm
VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build linux-amd64 linux-arm64 clean

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='$(LDFLAGS)' -o $(BINARY) $(PKG)

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='$(LDFLAGS)' -o dist/tspm-linux-amd64 $(PKG)

linux-arm64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='$(LDFLAGS)' -o dist/tspm-linux-arm64 $(PKG)

clean:
	rm -rf dist $(BINARY)
