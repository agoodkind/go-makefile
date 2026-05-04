.PHONY: build build-root build-staticcheck \
	build-check build-check-root build-check-staticcheck \
	lint lint-root lint-staticcheck lint-tools lint-golangci-baseline \
	lint-golangci-baseline-root lint-golangci-baseline-staticcheck \
	lint-format lint-format-root lint-format-staticcheck lint-gocyclo \
	fmt fmt-root fmt-staticcheck vet vet-root vet-staticcheck test test-root test-staticcheck \
	govulncheck govulncheck-root govulncheck-staticcheck \
	staticcheck-extra staticcheck-extra-root staticcheck-extra-staticcheck \
	staticcheck-extra-baseline staticcheck-extra-baseline-root \
	staticcheck-extra-baseline-staticcheck \
	check

GO_MK := go.mk
GOLANGCI_LINT ?= golangci-lint
ROOT_GO_MK := $(MAKE) -f $(GO_MK)
STATICCHECK_GO_MK := $(MAKE) -C staticcheck -f ../$(GO_MK)
ROOT_GOLANGCI_ARGS := GOLANGCI_LINT_FLAGS="-c golangci-template.yml" GOLANGCI_LINT_TARGETS=.
STATICCHECK_GOLANGCI_ARGS := GOLANGCI_LINT_FLAGS="-c ../golangci-template.yml"

.DEFAULT_GOAL := check

build: build-check build-root build-staticcheck

build-root:
	go build .

build-staticcheck:
	$(STATICCHECK_GO_MK) BUILD_CHECKS=false build

build-check: build-check-root build-check-staticcheck

build-check-root: vet-root lint-tools lint-root lint-format-root lint-gocyclo staticcheck-extra-root govulncheck-root

build-check-staticcheck:
	$(STATICCHECK_GO_MK) $(STATICCHECK_GOLANGCI_ARGS) build-check

lint: lint-tools lint-root lint-format lint-gocyclo staticcheck-extra lint-staticcheck

lint-golangci-baseline: lint-golangci-baseline-root lint-golangci-baseline-staticcheck

lint-golangci-baseline-root:
	$(ROOT_GO_MK) $(ROOT_GOLANGCI_ARGS) lint-golangci-baseline

lint-golangci-baseline-staticcheck:
	$(STATICCHECK_GO_MK) $(STATICCHECK_GOLANGCI_ARGS) lint-golangci-baseline

lint-root:
	$(ROOT_GO_MK) $(ROOT_GOLANGCI_ARGS) lint-golangci

lint-staticcheck:
	$(STATICCHECK_GO_MK) $(STATICCHECK_GOLANGCI_ARGS) lint-golangci

lint-tools:
	$(ROOT_GO_MK) lint-tools

lint-format: lint-format-root lint-format-staticcheck

lint-format-root:
	$(GOLANGCI_LINT) fmt --diff -c golangci-template.yml .

lint-format-staticcheck:
	cd staticcheck && $(GOLANGCI_LINT) fmt --diff -c ../golangci-template.yml ./...

lint-gocyclo:
	$(ROOT_GO_MK) lint-gocyclo

fmt: fmt-root fmt-staticcheck

fmt-root:
	$(GOLANGCI_LINT) fmt -c golangci-template.yml .

fmt-staticcheck:
	cd staticcheck && $(GOLANGCI_LINT) fmt -c ../golangci-template.yml ./...

vet: vet-root vet-staticcheck

vet-root:
	go vet .

vet-staticcheck:
	$(STATICCHECK_GO_MK) vet

test: test-root test-staticcheck

test-root:
	go test .

test-staticcheck:
	$(STATICCHECK_GO_MK) test

govulncheck: govulncheck-root govulncheck-staticcheck

govulncheck-root:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck .

govulncheck-staticcheck:
	$(STATICCHECK_GO_MK) govulncheck

staticcheck-extra: staticcheck-extra-root staticcheck-extra-staticcheck

staticcheck-extra-root:
	$(ROOT_GO_MK) staticcheck-extra

staticcheck-extra-staticcheck:
	$(STATICCHECK_GO_MK) staticcheck-extra

staticcheck-extra-baseline: staticcheck-extra-baseline-root staticcheck-extra-baseline-staticcheck

staticcheck-extra-baseline-root:
	$(ROOT_GO_MK) staticcheck-extra-baseline

staticcheck-extra-baseline-staticcheck:
	$(STATICCHECK_GO_MK) staticcheck-extra-baseline

check: build test
