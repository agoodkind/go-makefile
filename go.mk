GO_MK_URL     := https://raw.githubusercontent.com/agoodkind/go-makefile/main/go.mk
GOLANGCI_URL  := https://raw.githubusercontent.com/agoodkind/go-makefile/main/golangci-template.yml
GORELEASER_URL := https://raw.githubusercontent.com/agoodkind/go-makefile/main/goreleaser-template.yaml

GO_MK_CACHE       := $(HOME)/.cache/go-makefile/go.mk
GOLANGCI_CACHE    := $(HOME)/.cache/go-makefile/golangci-template.yml
GORELEASER_CACHE  := $(HOME)/.cache/go-makefile/goreleaser-template.yaml

.PHONY: lint fmt vet test govulncheck check sync

GOLANGCI_LINT ?= golangci-lint
GOFUMPT       ?= gofumpt
GOIMPORTS     ?= goimports

# Auto-bootstrap .golangci.yml if missing.
.golangci.yml:
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GOLANGCI_URL)" -o "$@"; then \
		mkdir -p "$(dir $(GOLANGCI_CACHE))" && cp "$@" "$(GOLANGCI_CACHE)"; \
	elif [ -f "$(GOLANGCI_CACHE)" ]; then \
		echo "warning: golangci fetch failed, using cached version" >&2; \
		cp "$(GOLANGCI_CACHE)" "$@"; \
	else \
		echo "error: golangci fetch failed and no cache available" >&2; \
		exit 1; \
	fi

# Auto-bootstrap .goreleaser.yaml if missing.
.goreleaser.yaml:
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GORELEASER_URL)" -o "$@"; then \
		mkdir -p "$(dir $(GORELEASER_CACHE))" && cp "$@" "$(GORELEASER_CACHE)"; \
	elif [ -f "$(GORELEASER_CACHE)" ]; then \
		echo "warning: goreleaser fetch failed, using cached version" >&2; \
		cp "$(GORELEASER_CACHE)" "$@"; \
	else \
		echo "error: goreleaser fetch failed and no cache available" >&2; \
		exit 1; \
	fi

lint: .golangci.yml
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
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GOLANGCI_URL)" -o "$(GOLANGCI_CACHE)"; then \
		echo "golangci-template updated in cache"; \
	fi
	@if curl -fsSL --connect-timeout 5 --max-time 10 "$(GORELEASER_URL)" -o "$(GORELEASER_CACHE)"; then \
		echo "goreleaser-template updated in cache"; \
	fi
