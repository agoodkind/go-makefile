.PHONY: build build-root build-staticcheck \
	lint lint-root lint-staticcheck lint-tools lint-format \
	fmt vet vet-root vet-staticcheck test test-root test-staticcheck \
	govulncheck govulncheck-root govulncheck-staticcheck \
	staticcheck-extra staticcheck-extra-root staticcheck-extra-staticcheck \
	check

GO_MK := go.mk
GOLANGCI_LINT ?= golangci-lint

.DEFAULT_GOAL := check

build: build-root build-staticcheck

build-root:
	go build .

build-staticcheck:
	$(MAKE) -C staticcheck -f ../$(GO_MK) build

lint: lint-tools lint-root lint-format staticcheck-extra lint-staticcheck

lint-root:
	$(GOLANGCI_LINT) run -c golangci-template.yml .

lint-staticcheck:
	cd staticcheck && $(GOLANGCI_LINT) run -c ../golangci-template.yml ./...

lint-tools:
	$(MAKE) -f $(GO_MK) lint-tools

lint-format:
	$(GOLANGCI_LINT) fmt --diff -c golangci-template.yml .

fmt:
	$(GOLANGCI_LINT) fmt -c golangci-template.yml .

vet: vet-root vet-staticcheck

vet-root:
	go vet .

vet-staticcheck:
	$(MAKE) -C staticcheck -f ../$(GO_MK) vet

test: test-root test-staticcheck

test-root:
	go test .

test-staticcheck:
	$(MAKE) -C staticcheck -f ../$(GO_MK) test

govulncheck: govulncheck-root govulncheck-staticcheck

govulncheck-root:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck .

govulncheck-staticcheck:
	$(MAKE) -C staticcheck -f ../$(GO_MK) govulncheck

staticcheck-extra: staticcheck-extra-root staticcheck-extra-staticcheck

staticcheck-extra-root:
	$(MAKE) -f $(GO_MK) staticcheck-extra

staticcheck-extra-staticcheck:
	$(MAKE) -C staticcheck -f ../$(GO_MK) staticcheck-extra

check: build vet lint test govulncheck
