# Credits & Acknowledgements

Praetor builds on the work of many open-source projects and published standards. This page credits every upstream origin.

## Borrowed ideas and adapted work

- **Caveman** — The core concept and syntax rules for token-compressed agent replies, implemented in the `caveman` skill and prose linter. MIT.
  https://github.com/JuliusBrussee/caveman
- **I Have ADHD (adhd-format)** — Formatting principles for high-focus, ADHD-friendly agent output, implemented in the `adhd-format` skill. MIT, © 2026 Ayoub Ghriss.
  https://github.com/ayghri/i-have-adhd

## Runtime dependencies

- **gopkg.in/yaml.v3** v3.0.1 — YAML parser for Go. MIT (ported libyaml files, © 2006–2011 Kirill Simonov) and Apache-2.0 (© 2011–2019 Canonical Ltd).
  https://github.com/go-yaml/yaml
- **markdownlint-cli2** 0.23.2 — Markdown linting engine. MIT, © David Anson.
  https://github.com/DavidAnson/markdownlint-cli2
- **micromark** 4.0.2 — Markdown parser. MIT.
  https://github.com/micromark/micromark
- **parse5** 8.0.1 — HTML parser. MIT.
  https://github.com/inikulin/parse5

## Container base image

- **Distroless** — Minimal container runtime. Apache-2.0, © Google LLC.
  https://github.com/GoogleContainerTools/distroless

## Standards and specifications implemented

- **NASA JPL "Power of 10" Rules** (Gerard J. Holzmann, 2006) — Basis for HISS invariants 01–10.
  https://spinroot.com/gerard/pdf/P10.pdf
- **Semantic Versioning 2.0.0** — Version string grammar and precedence.
  https://semver.org
- **Conventional Commits 1.0.0** — Commit message structure. MIT, © 2018 Conventional Changelog.
  https://www.conventionalcommits.org
- **Keep a Changelog 1.1.0** — Changelog section types. MIT, © 2014 Olivier Lacan.
  https://keepachangelog.com
- **Developer Certificate of Origin 1.1** — Commit sign-off contract. © 2004, 2006 The Linux Foundation.
  https://developercertificate.org
- **SLSA v1.0** — Supply-chain provenance levels. Community Specification License 1.0.
  https://slsa.dev
- **in-toto Attestation Framework v1** — Statement envelope format. Apache-2.0, © 2021 in-toto Developers.
  https://github.com/in-toto/attestation
- **CycloneDX 1.5** — Software Bill of Materials schema. Apache-2.0, © OWASP Foundation.
  https://cyclonedx.org
- **SPDX** — License identifiers and SBOM format.
  https://spdx.org
- **Model Context Protocol (MCP)** — Agent tool protocol.
  https://modelcontextprotocol.io
- **Schema.org** — Structured data vocabulary (TechArticle, SoftwareSourceCode). CC-BY-SA-3.0.
  https://schema.org
- **EditorConfig** — Cross-editor formatting configuration.
  https://editorconfig.org
- **Architectural Decision Records** — Decision documentation format (after Michael Nygard, 2011).
- **MADR (Markdown Any Decision Records)** — ADR format template. MIT OR CC0-1.0.
  https://adr.github.io/madr/
- **AGENTS.md Convention** — Open standard for AI coding agents. MIT, © 2025 OpenAI.
  https://agents.md/

## Build and CI tooling

- **Lefthook** — Git hooks manager. MIT, © 2019 Arkweid.
  https://github.com/evilmartians/lefthook
- **GoReleaser** — Release automation. MIT, © 2016–2026 Carlos Becker.
  https://goreleaser.com
- **golangci-lint** — Go linter aggregator. GPL-3.0-only.
  https://golangci-lint.run
- **Semgrep** — Static analysis. LGPL-2.1-only.
  https://semgrep.dev
- **MkDocs** — Documentation generator. BSD-2-Clause, © Tom Christie.
  https://www.mkdocs.org
- **MkDocs Material** — Documentation theme. MIT, © Martin Donath.
  https://squidfunk.github.io/mkdocs-material/
- **Cosign** (Sigstore) — Container/artifact signing. Apache-2.0.
  https://github.com/sigstore/cosign
- **Syft** (Anchore) — SBOM generation. Apache-2.0.
  https://github.com/anchore/syft
- **Renovate** — Dependency updates. AGPL-3.0-only.
  https://github.com/renovatebot/renovate
- **FSFE reuse-action** — REUSE compliance CI. GPL-3.0-or-later.
  https://github.com/fsfe/reuse-action
