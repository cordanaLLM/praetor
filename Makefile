.PHONY: all build test stress fuzz audit compile-context compile-context-verify lint verify-all clean hooks setup

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

stress:
	go test -v -race ./internal/stress/...

fuzz:
	go test -fuzz=FuzzHissScan -fuzztime=5s ./internal/hiss/...
	go test -fuzz=FuzzCompilerTranspile -fuzztime=5s ./internal/compiler/...
	go test -fuzz=FuzzBaselineRatchet -fuzztime=5s ./internal/baseline/...
	go test -fuzz=FuzzValidateJSONLD -fuzztime=5s ./internal/seo/...
	go test -fuzz=FuzzValidateRobotsTxt -fuzztime=5s ./internal/seo/...
	go test -fuzz=FuzzASTMerge -fuzztime=5s ./internal/astmerge/...
	go test -fuzz=FuzzChangelogRender -fuzztime=5s ./internal/changelog/...
	go test -fuzz=FuzzLSPHandleMessage -fuzztime=5s ./cmd/standards-lsp/...

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

hooks:
	@lefthook install

setup: build hooks compile-context

clean:
	rm -rf $(BIN_DIR)
