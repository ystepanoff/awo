.PHONY: all build test vet fmt lint clean install tidy

GO        ?= go
BINARY    ?= awo
PKG       ?= ./...
CMD       ?= ./cmd/awo

all: build

build:
	$(GO) build -o $(BINARY) $(CMD)

install:
	$(GO) install $(CMD)

test:
	$(GO) test $(PKG)

vet:
	$(GO) vet $(PKG)

fmt:
	$(GO) fmt $(PKG)

# `make lint` runs the checks every contributor is expected to pass
# locally. golangci-lint is optional: if it is on $PATH we run it,
# otherwise we fall back to `gofmt -l` + `go vet`. CI runs the same
# combination, so passing locally is a reliable signal.
lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(PKG); \
	else \
		echo "golangci-lint not installed, falling back to gofmt -l"; \
		test -z "$$(gofmt -l . | tee /dev/stderr)"; \
	fi

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)
