.PHONY: all build test stress fuzz audit compile-context compile-context-verify lint vuln sec flavor-audit state-audit dedupe topology-audit verify-all clean hooks setup

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
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

vuln:
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	elif [ -x "$$(go env GOPATH)/bin/govulncheck" ]; then \
		"$$(go env GOPATH)/bin/govulncheck" ./...; \
	else \
		echo "govulncheck not found; installing..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest && "$$(go env GOPATH)/bin/govulncheck" ./...; \
	fi

# gosec runs with ZERO exclusions: .gosec.json carries an empty exclude list and every
# finding is fixed or carries a per-line "#nosec Gxxx -- <reason>" justification.
sec:
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -conf .gosec.json ./...; \
	elif [ -x "$$(go env GOPATH)/bin/gosec" ]; then \
		"$$(go env GOPATH)/bin/gosec" -conf .gosec.json ./...; \
	else \
		echo "gosec not found; installing..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest && "$$(go env GOPATH)/bin/gosec" -conf .gosec.json ./...; \
	fi

flavor-audit:
	go run ./cmd/standardsctl flavor audit .

state-audit:
	go run ./cmd/standardsctl state audit .

dedupe:
	go run ./cmd/standardsctl dedupe scan .

topology-audit:
	@if [ -d "$$HOME/dev" ]; then go run ./cmd/standardsctl topology audit "$$HOME/dev"; fi

verify-all: compile-context-verify test audit lint vuln sec flavor-audit state-audit dedupe topology-audit
	@echo "All standards verification gates passed cleanly."

hooks:
	@lefthook install

setup: build hooks compile-context

clean:
	rm -rf $(BIN_DIR)
