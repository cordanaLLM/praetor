# Changelog

## [Unreleased]

### Added

- Configurable local and pinned-public dogfood capability discovery through shared
  CLI and MCP adapters, bounded evidence, deduplicated review candidates, and a
  100-repository cohort. Incomplete observations stay outside candidate ranking.
- Native discovery now covers Meson markers and HISS-dispatched CXX, CUDA, and
  HIP sources, while recording observed CUDA-header, Metal, Objective-C++, and shader
  extensions as unsupported adapter candidates.

- Configurable agent checkpoint cadence for reviewed commits, gated pushes and
  draft PR visibility, with a shared Lefthook check and native lifecycle adapter.
- Draft creation support in the existing GitHub forge request contract.

### Fixed

- Preflight composed agent context before writing canonical instructions, so
  projection-budget failures preserve existing canonical and vendor files.
- Skill audits retain bounded per-root outcomes and fail invalid or incomplete
  scans before optional hygiene actions.
- Public dogfood snapshots stream file hashes under explicit bounded input
  budgets shared by suite and discovery configs. Native metadata planning uses
  the same configured limits throughout repeated adoption and discovery readback;
  reports retain budgets and incomplete-input diagnostics.
- Checkpoint publication distinguishes local advances from remote-ahead and
  divergent branches, and reports missing or shallow history before suggesting
  a push.
- Adoption CLI and MCP distinguish observed baseline debt from skipped or failed
  scans and describe dry-run files as planned. Bare repository inventory treats
  working-tree privacy probes as not applicable.
- Resolve security scan scope from actual Go packages so private dogfood corpora
  stay outside project gates; first-party findings still fail with zero exclusions.

- Repair HISS Semgrep HTTP, Python eval and Rust test-boundary matching; add pinned
  real-engine regression checks. Document remaining enforcement gaps and staged
  bidirectional module contracts without replacing the HISS policy.

### Changed

- Garbage collection requires explicit release and retention checks, confines
  complete scans, preserves unpublished worktree branches, and reports partial
  failures. Shared Go test cache clearing is now opt-in.
- **Breaking:** GC dry-runs populate planned fields instead of completed deletion
  fields; ordinary worktree removal preserves branches. GC no longer prunes Git
  administration or sweeps repository-root and `bin/` files automatically.
  Migration: supply each quiescent resource with `--released-path`, consume
  `planned_*` report fields for previews, and handle nonzero partial-result exits.
  See [garbage collection](docs/guides/garbage-collection.md).


## [Unreleased] - 2026-09-11

### Changed

- Migrate Go module and repo identity to cordanaLLM/praetor and enforce strict identity audit

## [Unreleased] - 2026-09-11

### Changed

- Strengthen Lefthook configuration with stage_fixed and direct standardsctl gating



All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased] - 2026-09-11

### Added

- Codify automated git hooks, pre-commit enforcement, and anti-direct-push gating
- Universal runner matrix routing (self-hosted ARC, Darwin/macOS GitHub-hosted, GPU/XPU targets)
- Paperclip agent runtime harness (`.paperclip/harness.json`, Rule 0 terminal disposition, AGit protocol)
- 5-stage pre-migration epics generation and cross-repo issue dependency DAG reconciliation
- Polyglot needs extraction (Go, Svelte/Node, Python, Rust, Native GPU) and demand deduplicator
- Universal builder and pre-build optimizer with framework asset compilation

- Framework demand & needs reporting platform (`standardsctl needs`) with Golusoris catalog and automated AST migration engine
- Adoption and governance lattice bootstrapping for `cordanaLLM/imago` and `cordanaLLM/nucleus`
- Comprehensive 3D dispatch and command integration tests in `cmd/standardsctl/standardsctl_test.go`
- Continuous fuzzing battery spanning 8 subsystems (`hiss`, `compiler`, `baseline`, `seo`, `astmerge`, `changelog`, `lsp`) passing > 21M executions
- Concurrency stress and generative property validation suite in `internal/stress/stress_test.go`
- Dedicated `fuzz` and `stress` targets in `Makefile`
- Dogfooded framework demand declarations (`.needs.yaml`) in `praetor` targeting upstream Golusoris capabilities
- Expanded canonical catalog in `internal/needs/catalog.go` for YAML serialization and community MCP servers

### Changed

- Hardened codebase to 100% compliance with NASA JPL Rule 4 (<= 60 LOC per function) across all production and test suites
- Ratified technical debt baseline down from 66 infractions to exactly 0 infractions in `.standards-baseline.json`
- Bounded all I/O loops and streams with explicit timeout contexts and loop termination predicates
- Eliminated all unhandled error assignments (`_ = ...`) and legacy panic invocations
- Thread-safe serialization of Git worktree operations in `internal/worktree` with `sync.Mutex` guarding concurrent CLI invocations
