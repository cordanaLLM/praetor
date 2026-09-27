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

The relation column says how much of the upstream reached praetor: *copied* means upstream text
or code is reproduced, and then `REUSE.toml` carries the upstream copyright and license for that
path; *adapted* means upstream rules or structure are rewritten in praetor's own words; *inspired*
means only the idea is taken. A skill derived from an upstream names it in its front matter as
`metadata.derived_from`, for example `.agents/skills/caveman/SKILL.md`. Projects by praetor's own
maintainer are not listed.

| Project | Praetor artifact | Relation | What changed | License |
| :-- | :-- | :-- | :-- | :-- |
| [Caveman](https://github.com/JuliusBrussee/caveman) | `caveman` skill, `.agents/skills/caveman/SKILL.md` | adapted | The name and the rules (drop articles, filler and hedges; write fragments; keep code and error text verbatim; invent no abbreviations; keep negations; fall back to plain wording when compression makes a line ambiguous) are rewritten for agent-to-agent traffic only, and the skill adds connective symbols, the brief and return shapes and the evidence bound. No upstream text is copied. | MIT, © 2026 Julius Brussee (the upstream skill; its BSL-1.1 engine is not used) |
| [Caveman](https://github.com/JuliusBrussee/caveman) | prose linter, `internal/caveman` (`praetorctl caveman check`) | adapted | The same rules checked mechanically: article density, filler and hedge words, sentence length, and the brief and return contracts. The Go code is praetor's own. | MIT, © 2026 Julius Brussee |

"Caveman" is a trademark of Julius Brussee; praetor uses the name to refer to the upstream rules
and is not affiliated with or endorsed by the Caveman project.

No upstream is traced for the `adhd-format` skill (`.agents/skills/adhd-format/SKILL.md`) or for
`social-text`, which inherits from it: no candidate project checked shares their text, so neither
is credited until its source is confirmed.

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
| [The Power of 10 rules](https://spinroot.com/gerard/pdf/P10.pdf) (Gerard J. Holzmann, NASA JPL; IEEE Computer 39(6):95–97, 2006, [doi:10.1109/MC.2006.212](https://doi.org/10.1109/MC.2006.212)) | The basis of the HISS invariants | — |
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
