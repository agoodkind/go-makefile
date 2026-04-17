.PHONY: lint fmt vet test govulncheck check release go-mk-sync

GO_MK_URL   := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE := $(HOME)/.cache/go-makefile/go.mk

GOLANGCI_LINT ?= golangci-lint
GOFUMPT       ?= gofumpt
GOIMPORTS     ?= goimports

ifndef CMD
.PHONY: build
build:
	go build ./...
endif

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOFUMPT) -w .
	$(GOIMPORTS) -w .

vet:
	go vet ./...

test:
	go test ./...

govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

check: build vet lint test govulncheck

# Local release with notarization. Requires notarize.env (gitignored).
# Copy notarize.env.example to notarize.env and fill in your 1Password paths.
release:
	@[ -f notarize.env ] || { echo "notarize.env not found. Copy notarize.env.example and fill in your 1Password op:// paths."; exit 1; }
	op run --env-file=notarize.env -- goreleaser release --clean

# Renamed from 'sync' to avoid conflicts with project-level Makefile sync targets.
go-mk-sync:
	@mkdir -p "$(dir $(GO_MK_CACHE))"
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_URL)" -o "$(GO_MK)"; then \
		cp "$(GO_MK)" "$(GO_MK_CACHE)"; \
		echo "go.mk updated"; \
	else \
		echo "error: go.mk fetch failed" >&2; \
		exit 1; \
	fi
