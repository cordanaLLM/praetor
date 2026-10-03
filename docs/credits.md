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
means only the idea is taken. A persona or skill derived from an upstream names it in its front
matter as `metadata.derived_from: "<url> (<license>)"`, for example
`.agents/skills/caveman/SKILL.md`. Projects by praetor's own maintainer are not listed.

`TestShippedUpstreamsAreCredited` (`internal/supplychain/upstream_credits_test.go`) holds the
declarations and this table together through `CheckUpstreamCredits`
(`internal/supplychain/upstream_credits.go`). Each declaration needs a row that links the same
URL, names the declaring file in a code span, and states the declared license first in its
license cell. A *copied* row also needs `LICENSES/` to hold the license text and `REUSE.toml` to
label the file with the license in the last annotation table whose `path` globs match it, the
only table REUSE 3.3 applies (`ReuseLabels`, `internal/supplychain/reuse.go`). A row naming a persona or skill that declares no upstream fails as
well.

| Project | Praetor artifact | Relation | What changed | License |
| :-- | :-- | :-- | :-- | :-- |
| [Caveman](https://github.com/JuliusBrussee/caveman) | `caveman` skill, `.agents/skills/caveman/SKILL.md` | adapted | The name and the rules (drop articles, filler and hedges; write fragments; keep code and error text verbatim; invent no abbreviations; keep negations; fall back to plain wording when compression makes a line ambiguous) are rewritten for agent-to-agent traffic only, and the skill adds connective symbols, the brief and return shapes and the evidence bound. No upstream text is copied. | MIT, © 2026 Julius Brussee: the terms of the upstream skill before Caveman 3.0.0, from which this skill was adapted (2026-09-18). Caveman 3.0.0 (2026-09-24) relicensed the upstream repository to Apache-2.0 |
| [Caveman](https://github.com/JuliusBrussee/caveman) | prose linter, `internal/caveman` (`praetorctl caveman check`) | adapted | The same rules checked mechanically: article density, filler and hedge words, sentence length, and the brief and return contracts. The Go code is praetor's own. | MIT, © 2026 Julius Brussee, as above |

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
| [markdownlint](https://github.com/DavidAnson/markdownlint) 0.41.1 | Markdown linting; the Markdown gate's manifest and lock are embedded and installed, and `verify.mjs` calls the library. Its configuration file keeps the name and the output format of [markdownlint-cli2](https://github.com/DavidAnson/markdownlint-cli2), which the gate ran before | MIT, © David Anson |
| [jsonc-parser](https://github.com/microsoft/node-jsonc-parser) 3.3.1 and [smol-toml](https://github.com/squirrelchat/smol-toml) 1.9.0 | Parse inline `markdownlint-configure-file` comments written as JSONC or TOML, pinned in the same lock | MIT, © Microsoft; BSD-3-Clause, © Squirrel Chat et al. |
| [micromark](https://github.com/micromark/micromark) 4.0.3 | Markdown parsing, pinned in the same lock | MIT |
| [micromark-extension-mdxjs](https://github.com/micromark/micromark-extension-mdxjs) 3.0.0 | MDX syntax for the same parser, pinned in the same lock | MIT, © 2020 Titus Wormer |
| [parse5](https://github.com/inikulin/parse5) 8.0.1 | HTML parsing, pinned in the same lock | MIT |
| [interfig](https://github.com/vectorize-io/hindsight/tree/ccfe85b4851957ac2adf88b4a9ddf9668b2882f1/hindsight-interfig) (`hindsight-interfig/` in vectorize-io/hindsight) | Draws the interactive figures on this site; vendored byte-identical at commit `ccfe85b4851957ac2adf88b4a9ddf9668b2882f1` in `tools/figures/third_party/interfig/`. The binaries embed its render source and the figure player that bundles it (`tools/figures/assets.go`) | MIT, © 2025 Vectorize AI, Inc. |
| [React](https://github.com/facebook/react) 19.3.0, with react-dom 19.3.0 and scheduler 0.28.0 | Bundled with interfig into the figure player (`tools/figures/dist/player.js`) that the binaries embed and this site loads | MIT, © Meta Platforms, Inc. and affiliates |

## Container and development images

| Project | Use | License |
| :-- | :-- | :-- |
| [Distroless](https://github.com/GoogleContainerTools/distroless) | Runtime base of the published container image (`Dockerfile`) and of the Go Dockerfile template (`templates/go/Dockerfile.distroless.tmpl`) | Apache-2.0 |
| [golang Docker Official Image](https://github.com/docker-library/golang) | Build stage of the devcontainer bootstrap (`.devcontainer/Dockerfile.praetor`) and of the Go Dockerfile template | BSD-3-Clause |
| [Dev Container images](https://github.com/devcontainers/images) | `mcr.microsoft.com/devcontainers/base`, the base of this repository's devcontainer and of the one adoption writes (`internal/devcontainer/bootstrap.go`) | MIT |
| [Dev Container Features](https://github.com/devcontainers/features) | The `common-utils`, `go` and `node` features the devcontainers install (`.devcontainer/devcontainer.json`, `internal/devcontainer/devcontainer.go`) | MIT |

## Documentation presets

The presets in `docs/presets/` are copied into an adopting repository and build its site with
these packages. Each row names the packages as the preset's lock lists them:
`docs/presets/mkdocs/requirements.in` and `docs/presets/starlight/package.json`.
`TestCreditsNameEveryPresetPackage` (`internal/supplychain/credits_test.go`) fails when a
package there is not named here.

| Project | Preset and use | License |
| :-- | :-- | :-- |
| [MkDocs](https://github.com/mkdocs/mkdocs) (`mkdocs`) | `docs/presets/mkdocs`: the site generator | BSD-2-Clause |
| [Material for MkDocs](https://github.com/squidfunk/mkdocs-material) (`mkdocs-material`) | `docs/presets/mkdocs`: the theme, whose `base.html` the preset's `overrides/main.html` extends | MIT |
| [mkdocs-minify-plugin](https://github.com/byrnereese/mkdocs-minify-plugin) (`mkdocs-minify-plugin`) | `docs/presets/mkdocs`: HTML, CSS and JavaScript minification | MIT |
| [PyMdown Extensions](https://github.com/facelessuser/pymdown-extensions) (`pymdown-extensions`) | `docs/presets/mkdocs`: the `pymdownx` Markdown extensions | MIT |
| [Astro](https://github.com/withastro/astro) (`astro`, `@astrojs/sitemap`) | `docs/presets/starlight`: the site framework and its sitemap integration | MIT |
| [Starlight](https://github.com/withastro/starlight) (`@astrojs/starlight`) | `docs/presets/starlight`: the documentation theme, whose head the preset's `src/components/SEOHead.astro` overrides | MIT |
| [sharp](https://github.com/lovell/sharp) (`sharp`) | `docs/presets/starlight`: image processing for Astro's image service | Apache-2.0 |
| [TypeScript](https://github.com/microsoft/TypeScript) (`typescript`) | `docs/presets/starlight`: type checking, a development dependency | Apache-2.0 |

## GitHub Actions

The workflows in `.github/workflows/` and the composite actions in `.github/actions/` run these
actions, and the CI templates adoption writes (`templates/<language>/*.tmpl`) reference the ones
marked as templates. `TestCreditsNameEveryWorkflowAction` (`internal/supplychain/credits_test.go`)
fails when one of those files uses an action that has no row here.

| Action | Use | License |
| :-- | :-- | :-- |
| [actions/cache](https://github.com/actions/cache) | Workflows; Go CI templates | MIT |
| [actions/checkout](https://github.com/actions/checkout) | Workflows; every CI template | MIT |
| [actions/deploy-pages](https://github.com/actions/deploy-pages) | Workflows | MIT |
| [actions/setup-go](https://github.com/actions/setup-go) | Workflows; Go CI templates | MIT |
| [actions/setup-java](https://github.com/actions/setup-java) | JVM CI template | MIT |
| [actions/setup-node](https://github.com/actions/setup-node) | Workflows; Node CI template | MIT |
| [actions/setup-python](https://github.com/actions/setup-python) | Workflows | MIT |
| [actions/upload-pages-artifact](https://github.com/actions/upload-pages-artifact) | Workflows | MIT |
| [anchore/sbom-action](https://github.com/anchore/sbom-action) | Workflows: installs Syft | Apache-2.0 |
| [Azure/setup-helm](https://github.com/Azure/setup-helm) | Workflows | MIT |
| [docker/login-action](https://github.com/docker/login-action) | Workflows | Apache-2.0 |
| [docker/setup-buildx-action](https://github.com/docker/setup-buildx-action) | Workflows | Apache-2.0 |
| [fsfe/reuse-action](https://github.com/fsfe/reuse-action) | Workflows | GPL-3.0-or-later |
| [goreleaser/goreleaser-action](https://github.com/goreleaser/goreleaser-action) | Workflows | MIT |
| [oven-sh/setup-bun](https://github.com/oven-sh/setup-bun) | Node CI template | MIT |
| [sigstore/cosign-installer](https://github.com/sigstore/cosign-installer) | Workflows | Apache-2.0 |
| [subosito/flutter-action](https://github.com/subosito/flutter-action) | Flutter CI template | MIT |

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
| [Renovate](https://github.com/renovatebot/renovate) | Dependency update proposals; `renovate.json` extends its built-in presets `config:recommended`, `:dependencyDashboard`, `:semanticCommits` and `:maintainLockFilesWeekly` | AGPL-3.0 |
| [REUSE](https://reuse.software) ([reuse-action](https://github.com/fsfe/reuse-action)) | License compliance check | GPL-3.0-or-later |

<!-- REUSE-IgnoreEnd -->
