# Load environment variables securely
-include .env
export

# The name of the compiled binary
BINARY = postsync

.PHONY: help build run test vet lint clean

# Default target
help: ## Display this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build    - Compile the project with CGO_ENABLED=1 (required for SQLite)"
	@echo "  run      - Build and run the project locally"
	@echo "  test     - Run all unit tests"
	@echo "  vet      - Run go vet to check for suspicious constructs"
	@echo "  lint     - Run golangci-lint (if installed)"
	@echo "  clean    - Remove the compiled binary"

build: ## Build the Go application
	CGO_ENABLED=1 go build -o $(BINARY) .

run: build ## Build and execute the application
	./$(BINARY)

test: ## Run unit tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run

clean: ## Remove compiled artifacts
	rm -f $(BINARY)
