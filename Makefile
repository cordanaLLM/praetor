.PHONY: all build test stress fuzz audit caveman-sources compile-context compile-context-verify editors-reference editors-reference-verify editors-verify lint vuln sec secrets nosec-justified hiss-coverage flavor-audit state-audit dedupe topology-audit vscode-test wiki-sync-test dco-check-test docs-lint docs-lint-test docs-surface verify-all clean hooks setup

BIN_DIR := bin
# Windows cannot execute an extension-less PE, so the binary is named for the host rather than
# for the developer's platform. Every other target derives from these, so the suffix is set once.
# The alias links carry the suffix in their target too: a link named standardsctl.exe that points
# at an extension-less praetorctl dangles, because the build never writes that file on Windows.
EXE_SUFFIX := $(if $(filter Windows_NT,$(OS)),.exe,)
PRAETORCTL := $(BIN_DIR)/praetorctl$(EXE_SUFFIX)
STANDARDSCTL := $(BIN_DIR)/standardsctl$(EXE_SUFFIX)
PRAETOR_MCP := $(BIN_DIR)/praetor-mcp$(EXE_SUFFIX)
STANDARDS_MCP := $(BIN_DIR)/standards-mcp$(EXE_SUFFIX)
PRAETOR_LSP := $(BIN_DIR)/praetor-lsp$(EXE_SUFFIX)
STANDARDS_LSP := $(BIN_DIR)/standards-lsp$(EXE_SUFFIX)
# CI obtains coverage from the same race run used by verify-all.
TEST_COVERPROFILE ?=

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -v -o $(PRAETORCTL) ./cmd/standardsctl
	@ln -sf praetorctl$(EXE_SUFFIX) $(STANDARDSCTL)
	go build -v -o $(PRAETOR_MCP) ./cmd/standards-mcp
	@ln -sf praetor-mcp$(EXE_SUFFIX) $(STANDARDS_MCP)
	go build -v -o $(PRAETOR_LSP) ./cmd/standards-lsp
	@ln -sf praetor-lsp$(EXE_SUFFIX) $(STANDARDS_LSP)

# -timeout replaces go test's ten-minute default per package: internal/dogfood reached
# 600 s under -race on the CI runner and panicked mid-suite.
test:
	go test -v -race -timeout 30m $(if $(TEST_COVERPROFILE),-covermode=atomic -coverprofile="$(TEST_COVERPROFILE)") ./...

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

# editors/neovim and editors/jetbrains are rendered by internal/editor (editor.ReferenceSet);
# regenerate them after a renderer change, never edit them by hand.
editors-reference:
	go run ./cmd/standardsctl editors reference

editors-reference-verify:
	go run ./cmd/standardsctl editors reference --verify

# The editor files this repository selects in .standards.yaml (editors:) must hold every value
# the generator manages; `praetorctl editors generate` repairs them (BUG-439).
editors-verify:
	go run ./cmd/standardsctl editors verify

# The component tables of THIRD-PARTY-NOTICES.md are rendered from go.mod, the root Dockerfile,
# tools/markdownlint/package-lock.json and the embedded figure engine (the interfig pin, the
# player's THIRD-PARTY-LICENSES.txt and tools/figures/package-lock.json;
# internal/supplychain/notices_sources.go); run this after a dependency bump or a player
# rebuild. `make test` fails while the committed tables are stale, and while the figure player row
# of docs/credits.md names a package at a version the figure lock does not install
# (supplychain.CheckCredits); edit that row by hand.
.PHONY: third-party-notices
third-party-notices:
	go run ./cmd/standardsctl sbom notices

# Nothing regenerates .needs.yaml on its own; this fails when the committed manifest is not
# what `needs scan --write` would write now. It gates this repository only: adopter audits
# do not run it, so an adopter's older manifest is not failed by a newer Praetor. The scan
# reads no operator settings (framework.targets), so a local run judges the manifest as CI
# does: an empty variable and an empty --manifest select no document.
.PHONY: needs-check
needs-check:
	PRAETOR_FLEET_CONFIG= PRAETOR_WORKSTATION_CONFIG= go run ./cmd/standardsctl needs scan --check --manifest=

audit:
	go run ./cmd/standardsctl audit

caveman-sources:
	go run ./cmd/standardsctl caveman check --configured-sources --root=.

# One gofmt check over every tracked Go file, leaving testdata fixtures out: the pre-commit
# hook runs the same check (checks.py gofmt_check) on the staged set, and CI calls this target.
.PHONY: fmt-check
fmt-check:
	$(HOOK_RUNNER) fmt-check

# The API compatibility gate (tools/apicompat/gate) builds only under its own tag, which keeps it
# out of an adopting module's ./...; vet it with that tag here, and .golangci.yml sets the same
# tag for the linters.
lint: fmt-check
	go vet ./...
	go vet -tags=apicompatgate ./tools/apicompat/gate/
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

# The security scanners and gitleaks are pinned in one place, tools/go/go.mod, which Renovate
# keeps current; `go tool` builds and caches exactly that version, whatever is on PATH.
GO_SECURITY_TOOL := go tool -modfile=tools/go/go.mod

# HISS-12 declares "zero credentials in Git history" and gitleaks as its mechanism, but
# gitleaks appeared only in the archetypes this engine scaffolds INTO other repositories:
# the invariant was enforced on adopters and never on praetor itself. It scans history, not
# just the working tree, which is what the axiom actually claims. Measured on this
# repository: 234 commits, 23 MB, 2.5s, zero findings -- cheap enough to be unconditional.
secrets:
	$(GO_SECURITY_TOOL) gitleaks detect --no-banner --redact --config .gitleaks.toml

vuln:
	$(GO_SECURITY_TOOL) govulncheck ./...

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
	python3 -B .config/lefthook/scripts/security_scope.py -- $(GO_SECURITY_TOOL) gosec -conf .gosec.json
	$(GO_SECURITY_TOOL) gosec -conf .gosec.json -tags apicompatgate ./tools/apicompat/gate/

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

# DEV-01..DEV-05 judge a workstation's dev root, which this repository cannot declare. The
# audit runs only against a root PRAETOR_DEV_ROOT (or --dev-root) names and otherwise prints
# a stated skip, so verify-all never depends on whatever sits in a contributor's <home>/dev.
topology-audit:
	go run ./cmd/standardsctl topology audit --skip-unconfigured

verify-all: adr-verify semgrep-test docs-assets-test github-app-test docs-lint-test portability-test notebook-test mcp-test dev-codex-hooks-test dev-install-test dev-schedule-test dev-repair-test wiki-sync-test adopt-sweep-test dco-check-test vscode-test mcp-probe compile-context-verify caveman-sources needs-check editors-reference-verify editors-verify test audit lint vuln sec secrets fuzz hiss-coverage flavor-audit state-audit dedupe topology-audit hooks-test
	@echo "All standards verification gates passed cleanly."

# Vendored interfig (docs/adr/0015-interactive-figures-from-vendored-interfig.md section 8): the
# sync script's own tests, the offline pin check, and upstream's tests. node expands the quoted
# glob itself, so no shell globbing is involved.
.PHONY: interfig-verify
verify-all: interfig-verify
interfig-verify:
	python3 -B scripts/test_sync_interfig.py
	python3 -B scripts/sync_interfig.py verify
	node --test 'tools/figures/third_party/interfig/upstream/src/*.test.ts'

.PHONY: vscode-test
vscode-test:
	npm ci --prefix editors/vscode --ignore-scripts
	npm test --prefix editors/vscode

.PHONY: semgrep-test
semgrep-test:
	python3 -B scripts/test_hiss_semgrep.py

.PHONY: notebook-test

.PHONY: docs-assets-test
docs-assets-test:
	python3 -B scripts/test_docs_assets.py

.PHONY: github-app-test
github-app-test:
	python3 -B scripts/test_github_app_permissions.py

# BEGIN praetor documentation gate
.PHONY: docs-lint docs-figures
verify-all: docs-lint docs-figures
docs-lint:
	@node tools/markdownlint/verify.mjs
docs-figures:
	@node tools/figures/build.mjs check
	@node tools/figures/build.mjs sources
# END praetor documentation gate

docs-lint: docs-surface
docs-surface:
	@node tools/docsurface/verify.mjs

docs-lint-test:
	node tools/markdownlint/verify.mjs --self-test
	node tools/docsurface/verify.mjs --self-test

# The MkDocs figures hook is the one Python part of the figure engine (ADR-0016, section 6): this
# tests it, replaying the fence and markup fixtures the Node checks replay. The checks themselves
# are Node: their tests run under docs-figures-check, `check` and `sources` under docs-figures.
.PHONY: docs-diagrams-test
verify-all: docs-diagrams-test
docs-diagrams-test:
	python3 -B tools/figures/test_mkdocs_hook.py

# Interactive figures (docs/adr/0015-interactive-figures-from-vendored-interfig.md): the steps
# only this repository runs, because they need the npm lock adopters never receive: the engine's
# unit tests (render core and checks) and type check, and the committed player in
# tools/figures/dist/ rebuilt from the lock and compared byte for byte, within its size budget
# (`bundle.mjs --check`; docs/adr/0016-figures-for-adopters.md, section 3). A hand-edited or stale
# player file fails it. The render check (`build.mjs check`: a fresh render compared byte for byte
# with the committed SVG and JSON) and the source check (`build.mjs sources`: hashes, sizes, markup,
# spec/JSON pairs, fence slugs, evidence, the README block) run once, in the managed docs-figures
# target above, which every adopter receives too (ADR-0016, sections 5 and 8).
.PHONY: docs-figures-check
verify-all: docs-figures-check
docs-figures-check:
	npm ci --prefix tools/figures --ignore-scripts --no-audit --no-fund
	npm --prefix tools/figures test
	npm --prefix tools/figures run typecheck
	node tools/figures/bundle.mjs --check

# The presets' JSON-LD must read identity from the site's config, never name this project:
# a source check always, and rendered MkDocs builds when mkdocs-material is installed.
.PHONY: docs-seo-presets-test
verify-all: docs-seo-presets-test
docs-seo-presets-test:
	python3 -B scripts/test_docs_seo_presets.py

# BUG-992: README.md and docs/ may name only CLI commands, subcommands and flags the code
# defines and repository paths that exist. A guide that stops matching unchanged code shows up
# in no diff, so the drift check (--base, run on pull requests) cannot see it. Every glob of
# docs_surfaces in .standards.yaml must select a file (docs/guides/documentation-drift.md).
.PHONY: docs-references
verify-all: docs-references
docs-references:
	go run ./cmd/standardsctl docs references --path=.

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

.PHONY: adopt-sweep-test
adopt-sweep-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_adopt_repos.py'

# The compliance workflow's DCO 1.1 gate. Its cases build throwaway repositories under a
# temporary directory and contact no remote.
dco-check-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_dco_check.py'

clean:
	rm -rf $(BIN_DIR)

# Hooks inspect the index or committed push refs in disposable snapshots. The full
# verify-all target above remains the repository-wide authority.
.PHONY: hook-cli hooks-check hooks-test portability-test check-staged changed-packages test-changed lint-changed sec-changed check-changed sandbox-verify
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

portability-test:
	python3 -B scripts/test_portability_selftest.py

hooks-test:
	python3 -B .config/lefthook/scripts/test_security_scope.py
	python3 -B .config/lefthook/scripts/test_hooks.py
	python3 -B .config/lefthook/scripts/test_checkpoint.py
	python3 -B scripts/test_checkpoint_hooks.py
	python3 -B scripts/test_praetor_hook.py

# The canonical hook sources (checkpoint.py and common.py, which adoption copies, and the
# vendorable praetor.yml) must pass black, flake8 and yamllint as an adopter's hooks run
# them, and so must the renderings of the hook templates adoption writes (lefthook.yml,
# block_evasion.py, committed under internal/adopt/testdata/emitted) and the documentation
# gate's locked YAML (praetor-docs.yml, markdownlint-cli2.yaml). The tools come from the
# hash-locked .config/hook-lint/requirements.txt. A missing or mismatched tool is a skip with its reason
# locally, and a failure where PRAETOR_HOOK_LINT_BIN names the pinned toolchain (CI).
# scripts/test_emitted_yaml_lint.py applies the same yamllint run to the other YAML Praetor
# emits into an adopted repository, reusing that gate's tool resolution.
.PHONY: hooks-lint
verify-all: hooks-lint
hooks-lint:
	python3 -B scripts/test_emitted_hook_lint.py
	python3 -B scripts/test_emitted_yaml_lint.py

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

.PHONY: adr-verify
adr-verify:
	go run ./cmd/standardsctl adr verify --path=.
