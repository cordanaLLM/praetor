# Developer Rules Research

This directory contains research on development rules, mechanical enforcement tooling, and governance policies across major open-source software engineering projects.

## Scope & Method

The research surveys 114 software projects across 20 distinct technical domains, capturing 1,339 rules.

### Batches & Consolidation
The dataset was gathered across five sequential batches (`r1.tsv` through `r5.tsv`) and consolidated into the canonical dataset [catalogue.tsv](catalogue.tsv). Individual batch files were retired into git history upon consolidation.

### Domains
The survey spans 20 software domains:
- Compilers, languages, and runtimes (e.g. CPython, Go, LLVM, Rust, OpenJDK, V8)
- Operating systems and kernels (e.g. Linux kernel, FreeBSD, OpenBSD, illumos, Zephyr, Redox OS)
- Databases and storage (e.g. PostgreSQL, SQLite, Redis, Valkey, RocksDB, MongoDB, Ceph)
- Cloud-native infrastructure (e.g. Kubernetes, Envoy, containerd, OpenTelemetry, Prometheus)
- Cryptography and security (e.g. BoringSSL, OpenSSL, libsodium, WireGuard, GnuPG)
- Package ecosystems and developer tools (e.g. Cargo, npm, Homebrew, Nixpkgs, Git, QEMU)
- Scientific computing, networking, browsers, and safety-critical standards (e.g. WebKit, seL4, MISRA C, NASA JPL)

*Note: FFmpeg is covered by a dedicated separate research leg.*

### Pinning Rule & Sampling Verification
Every row records the source URL and revision where the rule was documented:
- **Pinned URLs (`pinned = yes`)**: 1,274 rows (95.1%) reference immutable revision identifiers (git commit SHA or release tag) embedded directly in the `source_url`.
- **Unpinned URLs (`pinned = no`)**: 65 rows (4.9%) document rules from projects that maintain live, unversioned developer policy portals (e.g. Debian Policy manuals, OpenBSD documentation, or FreeBSD project guides).
- **Sampling Validation**: A random sample of 12 pinned URLs across distinct domains was verified against live upstream servers via HTTP GET requests. All 12 of 12 sampled URLs returned HTTP 200 OK with the target revision present in the fetched document.

## Taxonomy & Summary Counts

The 1,339 rules are categorized across 11 functional disciplines:
- **Style** (367 rules, 112 projects): Formatting, indentation, line lengths, naming conventions, and language constructs.
- **License** (143 rules, 108 projects): SPDX tags, copyright banners, licensing compatibility, and Contributor License Agreements.
- **Commit** (138 rules, 81 projects): Commit subjects, body wrapping, atomic bisectability, and trailers.
- **Review** (138 rules, 94 projects): Code review quorum, area maintainer approvals, and issue checklists.
- **Testing** (138 rules, 102 projects): Unit tests, regression test suites, continuous fuzzing, and conformance suites.
- **Toolchain** (103 rules, 76 projects): Compiler flags, warning thresholds, lockfile pinning, and build reproducibility.
- **Security** (93 rules, 56 projects): Banned unsafe functions, secret scanning, sandboxing, and vulnerability disclosure.
- **API** (91 rules, 64 projects): ABI compatibility, symbol versioning, breaking change deprecation, and package boundaries.
- **CI** (58 rules, 53 projects): Presubmit gates, try builders, merge queues, and platform matrices.
- **Docs** (47 rules, 38 projects): Structured docstrings, Markdown link checking, manual pages, and documentation drift.
- **Release** (23 rules, 23 projects): Changelog fragments, backport targeting, and artifact signing.

These rules consolidate into **42 cross-project rule classes**, mapped against Praetor's own repository in [gap-matrix.md](gap-matrix.md):
- **Covered**: 19 classes
- **Partial**: 19 classes
- **Gap**: 3 classes
- **Not Applicable (N.A.)**: 1 class

Coverage citations were checked against source revision `247ae99da` on 2026-11-03. The correction checks repository paths and the named functions, rules, jobs or steps; repository gates and live forge settings were not exercised in this lane. The matrix separates engine capabilities from repository activation and identifies missing enforcement, including tests per change and security disclosure policy.

## How to Read the Gap Matrix

The companion document [gap-matrix.md](gap-matrix.md) evaluates Praetor's implementation against each rule class:
- **Class**: The standardized rule concept across projects.
- **Projects (count)**: The number of projects in the catalogue that hold this rule.
- **Their mechanical forms**: Concrete tools and configurations observed in peer projects (e.g. `gofmt`, `clang-format`, `towncrier`, `checkpatch.pl`, `libabigail`).
- **Our coverage (file/check)**: Repository-relative paths with the exact function, rule, job, step or configuration key, plus any enforcement limits.
- **Status**:
  - `covered`: Gate wiring exists in the checked repository source; this round does not certify successful gate execution or live forge configuration.
  - `partial`: Enforced for a subset of languages or surfaces, or related checks exist while the stated requirement or repository activation remains incomplete.
  - `gap`: Common industry standard with no active enforcement in Praetor.
  - `n.a.`: Not applicable by declared project policy (e.g. DCO 1.1 adopted instead of a corporate CLA).
- **Proposed check**: Concrete tool recommendation, hook stage (`pre-commit`, `commit-msg`, `pre-push`, `CI`), and execution cost.

Gaps and partials are ordered by defect-detection efficiency: defects prevented before push divided by execution latency.

## Glossary of Observed Tools

To assist newcomers, key tools and mechanisms referenced in the matrix include:
- `actionlint`: Static checker for GitHub Actions workflow definitions.
- `changie` / `towncrier`: Utilities that assemble release changelogs from isolated pull-request fragments in a directory.
- `checkpatch.pl`: Linux kernel script checking patch formatting, commit styles, and common C anti-patterns.
- `codespell` / `typos`: Fast source code spelling and typo analyzers.
- `DCO` (Developer Certificate of Origin): Standardized commit trailer (`Signed-off-by: Name <email>`) certifying legal contribution rights.
- `editorconfig-checker`: Universal tool validating whitespace and indentation across file types against `.editorconfig`.
- `gofmt`: Official Go formatting tool enforcing canonical indentation (tabs) and spacing.
- `gosec`: Security scanner inspecting Go Abstract Syntax Trees (AST) for common security vulnerabilities.
- `govulncheck`: Official Go toolchain vulnerability analyzer querying the Go Vulnerability Database.
- `hadolint`: Dockerfile linter enforcing best practices and Docker image size efficiency.
- `libabigail`: C and C++ ELF library binary analysis suite detecting Application Binary Interface (ABI) breakages.
- `reprotest`: Build tool verifying bit-for-bit reproducible compilation across varying environments and timestamps.
- `REUSE`: Free Software Foundation Europe standard for unambiguous, machine-readable licensing declarations.
- `shellcheck`: Static analysis tool identifying bugs and syntax errors in POSIX and bash shell scripts.
- `yamllint`: Linter verifying syntax, formatting, and key uniqueness across YAML files.
