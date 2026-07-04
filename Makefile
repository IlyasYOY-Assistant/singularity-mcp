.DEFAULT_GOAL := check

GO ?= go

.PHONY: check fix test vet generate install version

check: fix vet test

fix:
	$(GO) fix ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

generate:
	$(GO) generate ./...
install:
	$(GO) install ./cmd/singularity-mcp


version:
	$(GO) run ./cmd/singularity-mcp -version
