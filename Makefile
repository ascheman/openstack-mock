# Makefile for github.com/ascheman/openstack-mock
# Common development targets: build, tidy, clean, etc.

# Configuration
APP := openstack-mock
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
GO := go

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

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
