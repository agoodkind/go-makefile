.PHONY: lint fmt vet test govulncheck check sync

GO_MK_URL   := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GO_MK_CACHE := $(HOME)/.cache/go-makefile/go.mk

GOLANGCI_LINT ?= golangci-lint
GOFUMPT       ?= gofumpt
GOIMPORTS     ?= goimports

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

check: vet lint test govulncheck

sync:
	@mkdir -p "$(dir $(GO_MK_CACHE))"
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GO_MK_URL)" -o "$(GO_MK)"; then \
		cp "$(GO_MK)" "$(GO_MK_CACHE)"; \
		echo "go.mk updated"; \
	else \
		echo "error: go.mk fetch failed" >&2; \
		exit 1; \
	fi
