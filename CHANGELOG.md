# Changelog

## [Unreleased]

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
