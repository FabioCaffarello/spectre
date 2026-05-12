# Makefile for the proxy-broker infra-service.
#
# Convenience targets for local development. The canonical build
# is `docker buildx bake proxy-broker` from the repo root (produces
# the multi-arch image used in Compose + the chart).
#
# Targets mirror `adapters/curl-impersonate/Makefile` shape where
# applicable; kubebuilder-specific targets from the control-plane
# Makefile are omitted (proxy-broker is a plain gRPC service, not
# a Kubernetes operator).

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

.PHONY: build
build: ## Compile the broker binary to ./bin/proxy-broker.
	go build -trimpath -ldflags="-s -w" -o bin/proxy-broker ./cmd/proxy-broker

.PHONY: test
test: ## Run all unit tests.
	go test ./...

.PHONY: lint
lint: vet ## Run vet (Go's first-line linter).

.PHONY: vet
vet: ## Run `go vet` across all packages.
	go vet ./...

.PHONY: tidy
tidy: ## Refresh go.mod / go.sum.
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
