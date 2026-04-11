.PHONY: lint fmt vet test govulncheck check

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
