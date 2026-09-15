.PHONY: all build test stress fuzz audit compile-context compile-context-verify lint vuln sec secrets nosec-justified hiss-coverage flavor-audit state-audit dedupe topology-audit vscode-test wiki-sync-test verify-all clean hooks setup

BIN_DIR := bin
PRAETORCTL := $(BIN_DIR)/praetorctl
STANDARDSCTL := $(BIN_DIR)/standardsctl
PRAETOR_MCP := $(BIN_DIR)/praetor-mcp
STANDARDS_MCP := $(BIN_DIR)/standards-mcp
PRAETOR_LSP := $(BIN_DIR)/praetor-lsp
STANDARDS_LSP := $(BIN_DIR)/standards-lsp
# CI obtains coverage from the same race run used by verify-all.
TEST_COVERPROFILE ?=

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
	go test -v -race $(if $(TEST_COVERPROFILE),-covermode=atomic -coverprofile="$(TEST_COVERPROFILE)") ./...

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
	go test -fuzz=FuzzBugRecordRoundTrip -fuzztime=5s ./internal/state/...

compile-context:
	go run ./cmd/standardsctl compile-context

compile-context-verify:
	go run ./cmd/standardsctl compile-context --verify

audit:
	go run ./cmd/standardsctl audit

lint:
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

# HISS-12 declares "zero credentials in Git history" and gitleaks as its mechanism, but
# gitleaks appeared only in the archetypes this engine scaffolds INTO other repositories:
# the invariant was enforced on adopters and never on praetor itself. It scans history, not
# just the working tree, which is what the axiom actually claims. Measured on this
# repository: 234 commits, 23 MB, 2.5s, zero findings -- cheap enough to be unconditional.
secrets:
	@if command -v gitleaks >/dev/null 2>&1; then \
		gitleaks detect --no-banner --redact; \
	elif [ -x "$$(go env GOPATH)/bin/gitleaks" ]; then \
		"$$(go env GOPATH)/bin/gitleaks" detect --no-banner --redact; \
	else \
		echo "gitleaks not found; installing..."; \
		go install github.com/zricethezav/gitleaks/v8@latest && "$$(go env GOPATH)/bin/gitleaks" detect --no-banner --redact; \
	fi

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
#
# That justification rule is checked rather than merely asserted: an unexplained suppression
# is a hidden finding, and a claim no target verifies is free to drift out of truth.
nosec-justified:
	@bad=$$(git grep -n '#nosec' -- '*.go' | grep -v ' -- ' || true); \
	if [ -n "$$bad" ]; then \
		echo "[FAIL] every #nosec must carry a 'Gxxx -- <reason>' justification:"; \
		echo "$$bad"; \
		exit 1; \
	fi; \
	echo "[PASS] all $$(git grep -h '#nosec' -- '*.go' | wc -l | tr -d ' ') #nosec suppressions carry a reason."

sec: nosec-justified
	@if command -v gosec >/dev/null 2>&1; then \
		python3 -B .config/lefthook/scripts/security_scope.py -- gosec -conf .gosec.json; \
	elif [ -x "$$(go env GOPATH)/bin/gosec" ]; then \
		python3 -B .config/lefthook/scripts/security_scope.py -- "$$(go env GOPATH)/bin/gosec" -conf .gosec.json; \
	else \
		echo "gosec not found; installing..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest && python3 -B .config/lefthook/scripts/security_scope.py -- "$$(go env GOPATH)/bin/gosec" -conf .gosec.json; \
	fi

flavor-audit: state-init
	go run ./cmd/standardsctl flavor audit .

.PHONY: state-init
state-init:
	go run ./cmd/standardsctl state init --if-absent .

state-audit: state-init
	go run ./cmd/standardsctl state audit .

dedupe:
	go run ./cmd/standardsctl dedupe scan .

# HISS-20: every enforcement claim is replayed against its fixture corpus, so a declared
# state cannot drift from what the rules actually do -- in either direction.
hiss-coverage:
	go run ./cmd/standardsctl hiss coverage --verify

topology-audit:
	@if [ -d "$$HOME/dev" ]; then go run ./cmd/standardsctl topology audit "$$HOME/dev"; fi

verify-all: semgrep-test notebook-test mcp-test dev-codex-hooks-test dev-install-test dev-schedule-test dev-repair-test wiki-sync-test vscode-test mcp-probe compile-context-verify test audit lint vuln sec secrets fuzz hiss-coverage flavor-audit state-audit dedupe topology-audit hooks-test
	@echo "All standards verification gates passed cleanly."

.PHONY: vscode-test
vscode-test:
	npm ci --prefix editors/vscode --ignore-scripts
	npm test --prefix editors/vscode

.PHONY: semgrep-test
semgrep-test:
	python3 -B scripts/test_hiss_semgrep.py

.PHONY: notebook-test
notebook-test:
	python3 -B scripts/test_notebooklm_export.py
	python3 -B scripts/test_planning_import.py

hooks:
	@lefthook install

setup: build hooks compile-context state-audit

# Development connections always compile this checkout; artifacts live in temp.
.PHONY: mcp-dev mcp-probe mcp-test
mcp-dev:
	python3 scripts/dev_mcp.py serve

mcp-probe:
	python3 scripts/dev_mcp.py probe

mcp-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_dev_mcp*.py'

.PHONY: dev-install dev-install-test
.PHONY: dev-codex-hooks-test
dev-codex-hooks-test:
	python3 -B scripts/test_dev_codex_hooks.py

dev-install:
	python3 scripts/dev_install.py

dev-install-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_dev_install.py'

.PHONY: dev-schedule-test
dev-schedule-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_dev_schedule.py'

.PHONY: dev-repair-test
dev-repair-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_dev_repair.py'

wiki-sync-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_sync_github_wiki.py'

clean:
	rm -rf $(BIN_DIR)

# Hooks inspect the index or committed push refs in disposable snapshots. The full
# verify-all target above remains the repository-wide authority.
.PHONY: hook-cli hooks-check hooks-test check-staged changed-packages test-changed lint-changed sec-changed check-changed sandbox-verify
HOOK_RUNNER := python3 .config/lefthook/scripts/hooks.py
HOOK_GO_SOURCES := $(shell git ls-files '*.go')
BASE ?= HEAD~1
REF ?= HEAD

hook-cli: $(PRAETORCTL)

$(PRAETORCTL): $(HOOK_GO_SOURCES) go.mod go.sum Makefile
	@mkdir -p $(BIN_DIR)
	go build -o $(PRAETORCTL) ./cmd/standardsctl

hooks-check:
	lefthook validate
	lefthook check-install

hooks-test:
	python3 -B .config/lefthook/scripts/test_security_scope.py
	python3 -B .config/lefthook/scripts/test_hooks.py
	python3 -B .config/lefthook/scripts/test_checkpoint.py
	python3 -B scripts/test_checkpoint_hooks.py

check-staged:
	$(HOOK_RUNNER) pre-commit

changed-packages:
	$(HOOK_RUNNER) changed packages "$(BASE)"

test-changed:
	$(HOOK_RUNNER) changed test "$(BASE)"

lint-changed:
	$(HOOK_RUNNER) changed lint "$(BASE)"

sec-changed:
	$(HOOK_RUNNER) changed sec "$(BASE)"

check-changed:
	$(HOOK_RUNNER) changed all "$(BASE)"

sandbox-verify:
	python3 .config/lefthook/scripts/sandbox.py "$(REF)"
