BINARY := grimoire
GOLANGCI_LINT_VERSION ?= v2.13.2

GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif
GOLANGCI_LINT := $(firstword $(wildcard $(GOBIN)/golangci-lint $(shell go env GOPATH)/bin/golangci-lint))
ifeq ($(GOLANGCI_LINT),)
GOLANGCI_LINT := golangci-lint
endif

.DEFAULT_GOAL := help

.PHONY: help setup build install test lint

help: ## Show the available targets
	@echo "Grimoire — make targets"
	@echo ""
	@grep -E '^[a-z]+:.*##' $(MAKEFILE_LIST) \
		| sed -E 's/^([a-z]+):.*## (.*)$$/  make \1|\2/' \
		| awk -F'|' '{printf "%-18s %s\n", $$1, $$2}'

setup: ## Download dependencies and install golangci-lint
	go mod download
	go mod verify
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "Installed golangci-lint $(GOLANGCI_LINT_VERSION) in $(GOBIN)"

build: ## Build ./grimoire
	go build -buildvcs=false -trimpath -o $(BINARY) .

install: ## Install grimoire in GOBIN
	go install -buildvcs=false -trimpath .
	@echo "Installed grimoire in $(GOBIN)"

test: ## Run all tests with the race detector
	go test -race ./...

lint: ## Apply formatting and automatic lint fixes
	@command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1 || { echo "golangci-lint is missing; run 'make setup' first"; exit 1; }
	gofmt -w -s .
	go fix ./...
	$(GOLANGCI_LINT) run --fix ./...
