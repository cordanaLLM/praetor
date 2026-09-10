.PHONY: all build test audit compile-context compile-context-verify lint verify-all clean

BIN_DIR := bin
STANDARDSCTL := $(BIN_DIR)/standardsctl
STANDARDS_MCP := $(BIN_DIR)/standards-mcp
STANDARDS_LSP := $(BIN_DIR)/standards-lsp

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -v -o $(STANDARDSCTL) ./cmd/standardsctl
	go build -v -o $(STANDARDS_MCP) ./cmd/standards-mcp
	go build -v -o $(STANDARDS_LSP) ./cmd/standards-lsp

test:
	go test -v -race ./...

compile-context:
	go run ./cmd/standardsctl compile-context

compile-context-verify:
	go run ./cmd/standardsctl compile-context --verify

audit:
	go run ./cmd/standardsctl audit

lint:
	go vet ./...

verify-all: compile-context-verify test audit lint
	@echo "All standards verification gates passed cleanly."

clean:
	rm -rf $(BIN_DIR)
