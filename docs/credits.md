# Credits & Acknowledgements

Praetor adapts ideas from, ships code from, and builds with the open-source projects and published
specifications below. Each entry links to its upstream source and names its license as stated there.

The release archives and the container image carry
[`THIRD-PARTY-NOTICES.md`](https://github.com/cordanaLLM/praetor/blob/main/THIRD-PARTY-NOTICES.md),
which lists every shipped third-party component with its version, license and copyright line and
reproduces the upstream license and notice texts, and the license texts in
[`LICENSES/`](https://github.com/cordanaLLM/praetor/tree/main/LICENSES).

<!-- REUSE-IgnoreStart -->

## Adapted work

| Project | What praetor takes from it | License |
| :-- | :-- | :-- |
| [Caveman](https://github.com/JuliusBrussee/caveman) | The rules for terse, token-compressed agent text, implemented as the `caveman` skill and the prose linter in `internal/caveman` | MIT |
| [I Have ADHD](https://github.com/ayghri/i-have-adhd) | The formatting principles (bottom line first, bold anchors, callouts) behind the `adhd-format` skill | MIT, © 2026 Ayoub Ghriss |

## Shipped in the binaries

| Project | Use | License |
| :-- | :-- | :-- |
| [Go](https://go.dev) standard library and runtime | Statically linked into every binary | BSD-3-Clause, © 2009 The Go Authors |
| [go-yaml v3](https://github.com/go-yaml/yaml) (`gopkg.in/yaml.v3` v3.0.1) | YAML decoding and encoding; the only Go module the binaries link | MIT (files ported from libyaml, © 2006–2011 Kirill Simonov) and Apache-2.0 (© 2011–2019 Canonical Ltd) |
| [markdownlint-cli2](https://github.com/DavidAnson/markdownlint-cli2) 0.23.2 | Markdown linting; its manifest and lock are embedded and installed for the Markdown gate | MIT, © David Anson |
| [micromark](https://github.com/micromark/micromark) 4.0.2 | Markdown parsing, pinned in the same lock | MIT |
| [micromark-extension-mdxjs](https://github.com/micromark/micromark-extension-mdxjs) 3.0.0 | MDX syntax for the same parser, pinned in the same lock | MIT, © 2020 Titus Wormer |
| [parse5](https://github.com/inikulin/parse5) 8.0.1 | HTML parsing, pinned in the same lock | MIT |

## Container base image

| Project | Use | License |
| :-- | :-- | :-- |
| [Distroless](https://github.com/GoogleContainerTools/distroless) | Runtime base of the published container image | Apache-2.0 |

## Shipped on this site

| Project | Use | License |
| :-- | :-- | :-- |
| [interfig](https://github.com/vectorize-io/hindsight/tree/ccfe85b4851957ac2adf88b4a9ddf9668b2882f1/hindsight-interfig) (`hindsight-interfig/` in vectorize-io/hindsight) | Draws the interactive figures on this site; vendored byte-identical at commit `ccfe85b4851957ac2adf88b4a9ddf9668b2882f1` in `third_party/interfig/` | MIT, © 2025 Vectorize AI, Inc. |

## Specifications and standards

| Specification | Where praetor uses it | License |
| :-- | :-- | :-- |
| [The Power of 10 rules](https://spinroot.com/gerard/pdf/P10.pdf) (Gerard J. Holzmann, NASA JPL, 2006) | The basis of the HISS invariants | — |
| [Semantic Versioning 2.0.0](https://semver.org) | Version comparison and release tags | CC BY 3.0 |
| [Conventional Commits 1.0.0](https://www.conventionalcommits.org) | Commit subjects and the commit check | MIT, © 2018 Conventional Changelog |
| [Keep a Changelog 1.1.0](https://keepachangelog.com) | Changelog sections | MIT, © 2014 Olivier Lacan |
| [Developer Certificate of Origin 1.1](https://developercertificate.org) | The `Signed-off-by` requirement | © 2004, 2006 The Linux Foundation |
| [SLSA v1.0](https://slsa.dev) | Release provenance | Community Specification License 1.0 |
| [in-toto Attestation Framework v1](https://github.com/in-toto/attestation) | The provenance statement format | Apache-2.0 |
| [CycloneDX](https://cyclonedx.org) and [SPDX](https://spdx.org) | SBOM formats and license identifiers | Apache-2.0 (CycloneDX); Community Specification License 1.0, older portions CC BY 3.0 (SPDX) |
| [Model Context Protocol](https://modelcontextprotocol.io) | The MCP servers | Apache-2.0 (moving from MIT; not yet relicensed parts stay MIT) |
| [Schema.org](https://schema.org) | JSON-LD in the documentation presets | CC BY-SA 3.0 |
| [EditorConfig](https://editorconfig.org) | Editor settings the adoption writes | BSD-2-Clause, © 2019 EditorConfig Team |
| [MADR](https://adr.github.io/madr/) | The ADR template, after Michael Nygard's decision records | MIT or CC0-1.0 |
| [AGENTS.md](https://agents.md/) | The canonical agent instruction file | MIT, © 2025 OpenAI |

## Build and CI tooling

These run in the build, the hooks or CI. None of them ships in the binaries.

| Project | Use | License |
| :-- | :-- | :-- |
| [Lefthook](https://github.com/evilmartians/lefthook) | Git hooks | MIT |
| [GoReleaser](https://goreleaser.com) | Release archives, the container image and checksums | MIT |
| [golangci-lint](https://golangci-lint.run) | Go linting | GPL-3.0 |
| [Semgrep](https://semgrep.dev) | Static analysis rules | LGPL-2.1 |
| [MkDocs](https://www.mkdocs.org) and [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) | This site | BSD-2-Clause; MIT |
| [Cosign](https://github.com/sigstore/cosign) | Keyless signing and verification | Apache-2.0 |
| [Syft](https://github.com/anchore/syft) | SBOM generation | Apache-2.0 |
| [Renovate](https://github.com/renovatebot/renovate) | Dependency update proposals | AGPL-3.0 |
| [REUSE](https://reuse.software) ([reuse-action](https://github.com/fsfe/reuse-action)) | License compliance check | GPL-3.0-or-later |

<!-- REUSE-IgnoreEnd -->
