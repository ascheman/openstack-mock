# Makefile for github.com/ascheman/openstack-mock
# Common development targets: build, tidy, clean, etc.

# Configuration
APP := openstack-mock
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
GO := go

# Docker configuration
DOCKER ?= docker
IMAGE ?= $(APP)
# Determine current git branch (fallback to "local" if not in a git repo)
BRANCH_RAW := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo local)
# Sanitize branch to be a valid Docker tag: lowercase, replace slashes/spaces with '-', and map others to '-'
BRANCH := $(shell echo $(BRANCH_RAW) | tr '[:upper:]' '[:lower:]' | tr '/ ' '--' | sed -E 's/[^a-z0-9._-]/-/g')

# By default, show help
.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: download tidy ## Download modules and tidy go.mod/go.sum

.PHONY: download
download: ## Download Go module dependencies
	$(GO) mod download

.PHONY: tidy
tidy: ## Tidy Go modules
	$(GO) mod tidy

.PHONY: fmt
fmt: ## Format code
	$(GO) fmt ./...

.PHONY: vet
vet: ## Vet code
	$(GO) vet ./...

.PHONY: build
build: $(BIN) ## Build the application

$(BIN): GOFLAGS ?=
$(BIN):
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN) .

.PHONY: run
run: build ## Build and run the application
	./$(BIN)

.PHONY: test
test: ## Run tests
	$(GO) test ./...

.PHONY: docker-build
PLATFORMS ?= linux/amd64,linux/arm64

docker-build: ## Build and push multi-platform (amd64,arm64) image tagged as latest and current branch
	$(DOCKER) buildx build --platform $(PLATFORMS) -t $(IMAGE):latest -t $(IMAGE):$(BRANCH) --push .

.PHONY: docker-push
docker-push: ## Alias for docker-build (multi-platform build & push)
	$(MAKE) docker-build

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
	go clean -testcache
