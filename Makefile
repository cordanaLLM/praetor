.PHONY: all build test audit compile-context compile-context-verify lint verify-all clean

BIN_DIR := bin
PRAETORCTL := $(BIN_DIR)/praetorctl
STANDARDSCTL := $(BIN_DIR)/standardsctl
PRAETOR_MCP := $(BIN_DIR)/praetor-mcp
STANDARDS_MCP := $(BIN_DIR)/standards-mcp
PRAETOR_LSP := $(BIN_DIR)/praetor-lsp
STANDARDS_LSP := $(BIN_DIR)/standards-lsp

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -v -o $(PRAETORCTL) ./cmd/standardsctl
	@ln -sf praetorctl $(STANDARDSCTL)
	go build -v -o $(PRAETOR_MCP) ./cmd/standards-mcp
	@ln -sf praetor-mcp $(STANDARDS_MCP)
	go build -v -o $(PRAETOR_LSP) ./cmd/standards-lsp
	@ln -sf praetor-lsp $(STANDARDS_LSP)

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
