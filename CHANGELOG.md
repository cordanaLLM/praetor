# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
