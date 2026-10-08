# Credits & Acknowledgements

Praetor adapts ideas from, ships code from, builds with and integrates with the open-source
projects, products and published specifications below. Each entry links to its upstream source
and names its license as stated there.

The release archives and the container image carry
[`THIRD-PARTY-NOTICES.md`](https://github.com/cordanaLLM/praetor/blob/main/THIRD-PARTY-NOTICES.md),
which lists every shipped third-party component with its version, license and copyright line and
reproduces the upstream license and notice texts, and the license texts in
[`LICENSES/`](https://github.com/cordanaLLM/praetor/tree/main/LICENSES).

<!-- REUSE-IgnoreStart -->

## How this page is kept

The tables are rendered from one curated list, `docs/credits.yaml`: one entry per third-party
item, with its upstream URL, its kind, its relation to praetor, the license its upstream LICENSE
file or registry entry states, and the repository paths that use it. The prose around the tables
is written by hand. After an edit to the list, regenerate the tables:

```sh
PRAETOR_UPDATE_GOLDEN=1 go test ./internal/supplychain -run TestCreditsPageIsRendered
```

`TestCreditsPageIsRendered` (`internal/supplychain/credits_page_test.go`) fails while this page
differs from what `RenderCreditsPage` (`internal/supplychain/credits_page.go`) writes from the
list.

The kind says what the item is: a dependency, tool, action, image, vendored file, adapted code,
inspiration, specification (standards included), integration, or font or icon. The relation says
how much of it reaches praetor:

| Relation | Meaning |
| :-- | :-- |
| shipped | Its code is in the release binaries or the container image. |
| vendored | Upstream files are reproduced in this repository under their own license. |
| adapted | Upstream rules or structure are rewritten in praetor's own words; no text is copied. |
| inspired | Only the idea is taken. |
| used by CI | It runs in the build, the hooks or CI and ships nowhere. |
| integrated | Praetor reads, writes or calls it, or files praetor emits into an adopting repository install it. |

A license is an SPDX expression, or one of three words: `proprietary` for a closed product or
service used under its provider's terms, `none` when the upstream states no license, and `unknown`
when the license could not be verified upstream. An identifier the SPDX License List deprecates is
refused: a GNU license is written with `-only` or `-or-later`, as the upstream's notice states.

An entry's `packages` name the inventory items it answers, each as `<ecosystem>:<identifier>`:
`go:` a Go module or tool path, `npm:` and `pypi:` a package name, `action:` an owner/repository,
`image:` and `feature:` a repository, and `download:` an id of the `downloads` list. A package
answers only items of its own ecosystem, so an npm package and a download of the same name are
two items. Its `match` terms are further words a path names the item by.

The credits gate, `CheckUpstreamCredits` (`internal/supplychain/upstream_credits.go`), holds the
list to the repository; `TestShippedUpstreamsAreCredited`
(`internal/supplychain/upstream_credits_test.go`) runs it on the checkout and fails when:

- an item the dependency inventory lists has no entry whose `packages` name it. The inventory,
  `ReadCreditInventory` (`internal/supplychain/notices_sources.go`), reads the direct
  requirements and the tool block of every `go.mod` (`tools/go/go.mod` holds the tools), the
  direct dependencies of every `package.json`, every pip `requirements*.in`, every
  `requirements*.txt` with no `.in` of its name beside it (a hand-pinned file such as
  `.config/semgrep/requirements.txt`; a `pip-compile` lock is read through its `.in`), the
  `uses:` lines of the workflows, composite actions and CI templates, every Dockerfile `FROM`,
  and the image and features of every `devcontainer.json`. It reads the files git lists for the checkout (tracked
  files and untracked ones git does not ignore), outside `node_modules`, `testdata` and hidden
  directories other than `.config`, `.devcontainer` and `.github`. What CI fetches outside a
  manifest, such as a release binary downloaded with curl, is declared under `downloads` in the
  list;
- a path an entry, an original or a download names is not a file, or no longer names the item as a
  whole word: by its packages and match terms, or by its name when it declares neither, so a
  generic name such as Continue needs a specific match term;
- a canonical persona or skill (`.agents/agents`, `.agents/skills`) neither declares
  `metadata.derived_from` nor is listed under `originals`;
- an entry states license `unknown` and no unexpired entry of the `exceptions` list in
  `.standards.yaml` with rule `credits` names one of its paths, or such an exception has expired or
  excuses nothing. The exceptions list and its rules are described in the
  [clang-tidy coverage guide](guides/clang-tidy-coverage.md#exceptions).

`make credits-check` runs these tests and the notices tests on their own. `ci filter` classes this
page, the list and `THIRD-PARTY-NOTICES.md` as documentation and the personas and skills as agent
text, and both select the documentation gates, so the light run of a documentation or agent-only
pull request runs the target (`.github/workflows/ci.yml`); every other run reaches the same tests
through `make verify-all`.

## Adapted work

A persona or skill derived from an upstream names it in its front matter as
`metadata.derived_from: "<url> (<license>)"`, for example `.agents/skills/caveman/SKILL.md`. The
gate requires an entry with the same URL, the declaring file among its paths, the same license
and a relation of *adapted*, *inspired* or *vendored*. A *vendored* derivation also needs
`LICENSES/` to hold the license text and `REUSE.toml` to label the file with the license in the
last annotation table whose `path` globs match it, the only table REUSE 3.3 applies
(`ReuseLabels`, `internal/supplychain/reuse.go`). An entry of one of those relations that names a
persona or skill declaring no upstream, or another one, fails as well. Projects by praetor's own
maintainer are not listed.

| Project | Praetor artifact | Relation | What changed | License |
| :-- | :-- | :-- | :-- | :-- |
| [Caveman](https://github.com/JuliusBrussee/caveman) | `caveman` skill, `.agents/skills/caveman/SKILL.md` | adapted | The name and the rules (drop articles, filler and hedges; write fragments; keep code and error text verbatim; invent no abbreviations; keep negations; fall back to plain wording when compression makes a line ambiguous) are rewritten for agent-to-agent traffic only, and the skill adds connective symbols, the brief and return shapes and the evidence bound. No upstream text is copied. | MIT, © 2026 Julius Brussee: the MIT terms of the upstream skill when this skill was adapted from it (2026-09-18), shipped byte for byte as `LICENSE` beside the skill from upstream commit 8dffbb260, the last licence text before the relicensing. Upstream commit 921eab8a8 relicensed the repository to Apache-2.0 on 2026-09-24, released as Caveman 3.0.0 on 2026-09-30; releases before 3.0.0 keep the terms they shipped with |
| [Caveman](https://github.com/JuliusBrussee/caveman) | prose linter, `internal/caveman` (`praetorctl caveman check`) | adapted | The same rules checked mechanically: article density, filler and hedge words, sentence length, and the brief and return contracts. The Go code is praetor's own. | MIT, © 2026 Julius Brussee, as above |
| [i-have-adhd](https://github.com/ayghri/i-have-adhd) | `adhd-format` skill, `.agents/skills/adhd-format/SKILL.md`; `social-text` skill, `.agents/skills/social-text/SKILL.md`, which inherits from it | adapted | The purpose: shape output for a reader with ADHD by leading with the next action, numbering steps, chunking and suppressing tangents. The rules are rewritten in praetor's own words for technical reports (bottom line first, bolded anchors, alert callouts, small diagrams), and no upstream word sequence is reused. `social-text` inherits the credit through the principles it takes from `adhd-format`. | MIT, © 2026 Ayoub Ghriss: the MIT text shipped byte for byte as `LICENSE` beside each skill, from upstream commit d1671755d, the last commit to change the licence text |
| [Documenting Architecture Decisions](https://www.cognitect.com/blog/2011/11/15/documenting-architecture-decisions) by Michael Nygard (2011), and [MADR](https://adr.github.io/madr/) | `adr-scaffold` skill, `.agents/skills/adr-scaffold/SKILL.md`, and the records in `docs/adr/` | inspired | The practice of one short, immutable record per decision and its shape: status, context, decision and consequences. Numbering, the lifecycle rules and the HISS framing are praetor's own. | CC0-1.0, waived by Cognitect; MADR is MIT OR CC0-1.0 |

"Caveman" is a trademark of Julius Brussee; praetor uses the name to refer to the upstream rules
and is not affiliated with or endorsed by the Caveman project.

The `adhd-format` skill and `social-text`, which inherits from it, are adapted from
[i-have-adhd](https://github.com/ayghri/i-have-adhd), a skill with the same purpose.

## Shipped in the binaries

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [Go](https://go.dev) standard library and runtime | Statically linked into every binary | dependency, shipped | BSD-3-Clause, © 2009 The Go Authors |
| [go-yaml v3](https://github.com/go-yaml/yaml) (`gopkg.in/yaml.v3` v3.0.1) | YAML decoding and encoding; the only Go module the binaries link | dependency, shipped | MIT AND Apache-2.0, MIT for the files ported from libyaml, © 2006–2011 Kirill Simonov; Apache-2.0 for the rest, © 2011–2019 Canonical Ltd |
| [SchemaStore Claude Code settings schema](https://github.com/SchemaStore/schemastore) (`claude-code-settings.json` at commit `ce64da2`) | Checks the Claude Code settings and hook files Praetor renders; vendored byte-identical under `internal/clientschema/vendor/` and embedded in the binaries (docs/guides/client-schemas.md) | vendored file, vendored | Apache-2.0, © SchemaStore contributors |
| [Gemini CLI settings schema](https://github.com/google-gemini/gemini-cli) (`schemas/settings.schema.json` at `v0.63.0`) | Checks the Gemini CLI settings and hook files Praetor renders; vendored byte-identical under `internal/clientschema/vendor/` and embedded in the binaries | vendored file, vendored | Apache-2.0, © Google LLC |
| [Codex config and hook schemas](https://github.com/openai/codex) (`codex-rs/core/config.schema.json` and `codex-rs/hooks/schema/generated/` at `rust-v0.162.0`) | Checks the Codex hook files and payload fixtures and generates the Codex hook event types (`internal/codexhook`); vendored byte-identical under `internal/clientschema/vendor/` and embedded in the binaries | vendored file, vendored | Apache-2.0, © OpenAI |
| [Model Context Protocol schema](https://github.com/modelcontextprotocol/modelcontextprotocol) (`schema/2025-11-25/schema.json`) | Checks the `standards-mcp` responses and generates the MCP message types (`internal/mcpwire`); vendored byte-identical under `internal/clientschema/vendor/` and embedded in the binaries | vendored file, vendored | MIT, © 2024–2025 Anthropic, PBC and contributors; the LICENSE file at tag 2025-11-25 states MIT |
| [opencode config schema](https://github.com/anomalyco/opencode) (hosted `https://opencode.ai/config.json`, observed beside release `v1.18.35`) | Checks the `opencode.json` MCP projection Praetor renders; vendored byte-identical under `internal/clientschema/vendor/` and embedded in the binaries | vendored file, vendored | MIT, © 2025 opencode |
| [markdownlint](https://github.com/DavidAnson/markdownlint) 0.41.1 | Markdown linting; the Markdown gate's manifest and lock are embedded and installed, and `verify.mjs` calls the library. Its configuration file keeps the name and the output format of [markdownlint-cli2](https://github.com/DavidAnson/markdownlint-cli2), which the gate ran before | dependency, integrated | MIT, © David Anson |
| [jsonc-parser](https://github.com/microsoft/node-jsonc-parser) 3.3.1 | Parses inline `markdownlint-configure-file` comments written as JSONC, pinned in the same lock | dependency, integrated | MIT, © Microsoft |
| [smol-toml](https://github.com/squirrelchat/smol-toml) 1.9.0 | Parses inline `markdownlint-configure-file` comments written as TOML, pinned in the same lock | dependency, integrated | BSD-3-Clause, © Squirrel Chat et al. |
| [js-yaml](https://github.com/nodeca/js-yaml) 5.4.2 | Parses YAML for the Markdown gate, pinned in the same lock | dependency, integrated | MIT, © 2011–2015 Vitaly Puzrin |
| [micromark](https://github.com/micromark/micromark) 4.0.3 | Markdown parsing, pinned in the same lock | dependency, integrated | MIT, © Titus Wormer |
| [micromark-extension-mdxjs](https://github.com/micromark/micromark-extension-mdxjs) 3.0.0 | MDX syntax for the same parser, pinned in the same lock | dependency, integrated | MIT, © 2020 Titus Wormer |
| [parse5](https://github.com/inikulin/parse5) 8.0.1 | HTML parsing, pinned in the same lock | dependency, integrated | MIT, © 2013–2019 Ivan Nikulin |
| [Dev Container CLI](https://github.com/devcontainers/cli) (`@devcontainers/cli` 0.89.0) | Builds a repository's devcontainer for the gate; the binaries embed its manifest and lock (`internal/devcontainer/cli.go`) and install it into the tool cache | tool, integrated | MIT, © Microsoft Corporation |
| [interfig](https://github.com/vectorize-io/hindsight/tree/ccfe85b4851957ac2adf88b4a9ddf9668b2882f1/hindsight-interfig) (`hindsight-interfig/` in vectorize-io/hindsight) | Draws the interactive figures on this site; vendored byte-identical at commit `ccfe85b4851957ac2adf88b4a9ddf9668b2882f1` in `tools/figures/third_party/interfig/`. The binaries embed its render source and the figure player that bundles it (`tools/figures/assets.go`) | vendored file, vendored | MIT, © 2025 Vectorize AI, Inc. Its vendored `package.json` names [Vite](https://github.com/vitejs/vite), [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react) and [Prettier](https://github.com/prettier/prettier) (MIT each) as development dependencies; no code of theirs is reproduced, and praetor's figure build does not install them |
| [React](https://github.com/facebook/react) 19.3.0, with react-dom 19.3.0 and scheduler 0.28.0 | Bundled with interfig into the figure player (`tools/figures/dist/player.js`) that the binaries embed and this site loads | dependency, shipped | MIT, © Meta Platforms, Inc. and affiliates |

## Container and development images

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [Distroless](https://github.com/GoogleContainerTools/distroless) | Runtime base of the published container image (`Dockerfile`) and of the Go Dockerfile template (`templates/go/Dockerfile.distroless.tmpl`) | image, shipped | Apache-2.0 |
| [golang Docker Official Image](https://github.com/docker-library/golang) | Build stage of the devcontainer bootstrap (`.devcontainer/Dockerfile.praetor`), of the Go Dockerfile template, and base of the development image (`docker/dev/Dockerfile`) | image, integrated | BSD-3-Clause |
| [Dev Container images](https://github.com/devcontainers/images) | `mcr.microsoft.com/devcontainers/base`, the base of this repository's devcontainer and of the one adoption writes (`internal/devcontainer/bootstrap.go`) | image, integrated | MIT |
| [Dev Container Features](https://github.com/devcontainers/features) | The `common-utils`, `go` and `node` features the devcontainers install (`.devcontainer/devcontainer.json`, `internal/devcontainer/devcontainer.go`) | image, integrated | MIT |

## Documentation presets

The presets in `docs/presets/` are copied into an adopting repository and build its site with
these packages, which the preset's lists pin: `docs/presets/mkdocs/requirements.in` and
`docs/presets/starlight/package.json`.

An adopter who copies the Starlight preset and runs `npm ci` receives every package of
`docs/presets/starlight/package-lock.json`, each with its own license file. The preset ships in
this repository only: praetor's binaries embed neither its manifest and lock nor the packages.
Most are under MIT, Apache-2.0, ISC or a BSD license. Two are weak copyleft, and their entries
below follow the lock: the prebuilt libvips that sharp installs per platform
(`@img/sharp-libvips-*`, also inside `@img/sharp-win32-*` and `@img/sharp-wasm32`) is
LGPL-3.0-or-later, and Lightning CSS with its platform binaries is
MPL-2.0. The lock also holds BlueOak-1.0.0, CC0-1.0, Python-2.0 and 0BSD packages. To count the
licenses the lock records:

```sh
jq -r '.packages[] | .license // empty' docs/presets/starlight/package-lock.json | sort | uniq -c
```

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [MkDocs](https://github.com/mkdocs/mkdocs) (`mkdocs`) | `docs/presets/mkdocs`: the site generator; it builds this site too | dependency, integrated | BSD-2-Clause |
| [Material for MkDocs](https://github.com/squidfunk/mkdocs-material) (`mkdocs-material`) | `docs/presets/mkdocs`: the theme, whose `base.html` the preset's `overrides/main.html` extends; this site uses it too. It bundles the icons and loads the fonts listed under Fonts and icons | dependency, integrated | MIT |
| [mkdocs-minify-plugin](https://github.com/byrnereese/mkdocs-minify-plugin) (`mkdocs-minify-plugin`) | `docs/presets/mkdocs`: HTML, CSS and JavaScript minification | dependency, integrated | MIT |
| [PyMdown Extensions](https://github.com/facelessuser/pymdown-extensions) (`pymdown-extensions`) | `docs/presets/mkdocs`: the `pymdownx` Markdown extensions | dependency, integrated | MIT |
| [Astro](https://github.com/withastro/astro) (`astro`, `@astrojs/sitemap`) | `docs/presets/starlight`: the site framework and its sitemap integration | dependency, integrated | MIT |
| [Starlight](https://github.com/withastro/starlight) (`@astrojs/starlight`) | `docs/presets/starlight`: the documentation theme, whose head the preset's `src/components/SEOHead.astro` overrides | dependency, integrated | MIT |
| [sharp](https://github.com/lovell/sharp) (`sharp`) | `docs/presets/starlight`: image processing for Astro's image service | dependency, integrated | Apache-2.0 AND LGPL-3.0-or-later, sharp itself is Apache-2.0; the prebuilt platform package npm installs beside it carries libvips and its dependencies under LGPL-3.0-or-later (`@img/sharp-libvips-*`, and inside `@img/sharp-win32-*` and `@img/sharp-wasm32`), so an adopter who copies the preset receives that LGPL binary with its license |
| [Lightning CSS](https://github.com/parcel-bundler/lightningcss) (`lightningcss`) | `docs/presets/starlight`: CSS processing in the site build | dependency, integrated | MPL-2.0, with its platform binaries `lightningcss-*`; Vite requires it, so the Starlight preset lock installs it |
| [TypeScript](https://github.com/microsoft/TypeScript) (`typescript`) | `docs/presets/starlight`: type checking, a development dependency; it also checks the figure tooling and builds the VS Code extension | dependency, integrated | Apache-2.0 |

## GitHub Actions

The workflows in `.github/workflows/` and the composite actions in `.github/actions/` run these
actions, and the CI templates adoption writes (`templates/<language>/*.tmpl`) reference the ones
whose use names a template.

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [actions/cache](https://github.com/actions/cache) | Workflows; Go CI templates | action, used by CI | MIT |
| [actions/checkout](https://github.com/actions/checkout) | Workflows; every CI template | action, used by CI | MIT |
| [actions/deploy-pages](https://github.com/actions/deploy-pages) | Workflows | action, used by CI | MIT |
| [actions/setup-go](https://github.com/actions/setup-go) | Workflows; Go CI templates | action, used by CI | MIT |
| [actions/setup-java](https://github.com/actions/setup-java) | JVM CI template | action, integrated | MIT |
| [actions/setup-node](https://github.com/actions/setup-node) | Workflows; Node CI template | action, used by CI | MIT |
| [actions/setup-python](https://github.com/actions/setup-python) | Workflows | action, used by CI | MIT |
| [actions/upload-pages-artifact](https://github.com/actions/upload-pages-artifact) | Workflows | action, used by CI | MIT |
| [anchore/sbom-action](https://github.com/anchore/sbom-action) | Workflows: installs Syft | action, used by CI | Apache-2.0 |
| [Azure/setup-helm](https://github.com/Azure/setup-helm) | Workflows: installs Helm | action, used by CI | MIT |
| [docker/login-action](https://github.com/docker/login-action) | Workflows | action, used by CI | Apache-2.0 |
| [docker/setup-buildx-action](https://github.com/docker/setup-buildx-action) | Workflows | action, used by CI | Apache-2.0 |
| [fsfe/reuse-action](https://github.com/fsfe/reuse-action) | Workflows: runs the REUSE check | action, used by CI | GPL-3.0-or-later |
| [goreleaser/goreleaser-action](https://github.com/goreleaser/goreleaser-action) | Workflows | action, used by CI | MIT |
| [oven-sh/setup-bun](https://github.com/oven-sh/setup-bun) | Node CI template | action, integrated | MIT |
| [sigstore/cosign-installer](https://github.com/sigstore/cosign-installer) | Workflows: installs Cosign | action, used by CI | Apache-2.0 |
| [subosito/flutter-action](https://github.com/subosito/flutter-action) | Flutter CI template | action, integrated | MIT |

## Integrations

Forges, model servers, services, toolchains and agent clients praetor reads, writes, calls or
configures. Praetor ships none of their code.

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [GitHub](https://github.com) | The forge praetor's issue, pull-request, ruleset, project and Actions commands drive through the GitHub REST and GraphQL APIs (`internal/forge/github.go`) | integration, integrated | proprietary, a hosted service used under GitHub's terms |
| [Gitea](https://github.com/go-gitea/gitea) | Forge provider: the forge commands against a Gitea API (`internal/forge/gitea.go`) | integration, integrated | MIT |
| [Forgejo](https://codeberg.org/forgejo/forgejo) | Forge provider through the Gitea-compatible API (`internal/forge/gitea.go`) | integration, integrated | GPL-3.0-or-later, from Forgejo 9.0; earlier releases are MIT |
| [GitLab](https://gitlab.com/gitlab-org/gitlab) | Forge provider: the forge commands against the GitLab API (`internal/forge/gitlab.go`) | integration, integrated | MIT, outside `ee/` and `jh/`, which carry their own licenses; `doc/` is CC BY-SA 4.0 |
| [Hindsight](https://github.com/vectorize-io/hindsight) | Memory server the `hindsight` commands and MCP tools read and write (`internal/hindsight`); interfig, above, comes from the same repository | integration, integrated | MIT, © 2025 Vectorize AI, Inc. |
| [LiteLLM](https://github.com/BerriAI/litellm) | Model gateway whose catalog the model router syncs and whose response-cost header repair runs read | integration, integrated | MIT, © 2023 Berri AI; `enterprise/` carries its own license |
| [Ollama](https://github.com/ollama/ollama) | Local model server whose installed models the router discovers (`internal/router/discovery.go`) | integration, integrated | MIT |
| [vLLM](https://github.com/vllm-project/vllm) | OpenAI-compatible model server whose models the router discovers (`internal/router/discovery.go`) | integration, integrated | Apache-2.0 |
| [OpenVINO](https://github.com/openvinotoolkit/openvino) | Named by the Python ML flavor, which audits PyTorch and OpenVINO pipelines (`internal/flavor/definitions.go`) | integration, integrated | Apache-2.0 |
| [Paperclip](https://github.com/paperclipai/paperclip) | Agent control plane the adoption policy, the harness (`internal/paperclip`) and the `paperclip-operate` skill integrate with; the skill's rules are praetor's own | integration, integrated | MIT |
| [NotebookLM](https://notebooklm.google) | Planning notebooks: praetor prepares source snapshots for it (`internal/notebook`) and exports to it (`scripts/notebooklm_export.py`) | integration, integrated | proprietary, a Google service used under Google's terms |
| [Zig](https://github.com/ziglang/zig) | The needs analyzer reads Zig builds (`build.zig`) and their package manifests (`build.zig.zon`) | integration, integrated | MIT |
| [Trivy](https://github.com/aquasecurity/trivy) | Vulnerability scanner the container, OS-image and infrastructure archetypes name; the generated devcontainers install its editor extension | integration, integrated | Apache-2.0 |
| [Node.js](https://github.com/nodejs/node) | Runs the Markdown gate, the figure build and the devcontainer CLI; praetorctl downloads the release `internal/devcontainer/cli/node.json` pins from nodejs.org | tool, integrated | MIT, © Node.js contributors; each release archive carries the licenses of the dependencies it bundles |
| [Git](https://git-scm.com) | praetorctl runs git for every repository read and write | tool, integrated | GPL-2.0-only |
| [Visual Studio Code](https://github.com/microsoft/vscode) | The praetor extension in `editors/vscode` runs in it, and adoption writes its workspace settings | integration, integrated | MIT, the Code - OSS source; Microsoft's Visual Studio Code builds ship under the Microsoft product license |
| [JetBrains IDEs](https://www.jetbrains.com/ides/) | `praetorctl editors generate` writes their inspection profile and workspace settings (`.idea/`) | integration, integrated | proprietary, JetBrains products used under their terms; the open-source builds of IntelliJ IDEA and PyCharm are Apache-2.0 under the JetBrains Open-Source Build Terms |
| [Neovim](https://github.com/neovim/neovim) | `praetorctl editors generate` writes its project configuration (`.nvim.lua`, `lua/standards.lua`) | integration, integrated | Apache-2.0 AND Vim, © Neovim contributors; the parts contributed under the Vim license keep it |
| [Zed](https://github.com/zed-industries/zed) | `praetorctl editors generate` writes its settings and tasks (`.zed/`) | integration, integrated | GPL-3.0-or-later, the editor's license; upstream marks its Apache-2.0 components |
| [Helix](https://github.com/helix-editor/helix) | `praetorctl editors generate` writes its configuration and languages (`.helix/`) | integration, integrated | MPL-2.0 |
| [GNU Emacs](https://www.gnu.org/software/emacs/) | `praetorctl editors generate` writes its directory variables (`.dir-locals.el`) | integration, integrated | GPL-3.0-or-later |
| [Fleet](https://www.jetbrains.com/fleet/) | `praetorctl editors generate` writes its settings and run configurations (`.fleet/`) | integration, integrated | proprietary, a JetBrains product used under its terms |
| [Sublime Text](https://www.sublimetext.com) | `praetorctl editors generate` writes its project file (`standards.sublime-project`) | integration, integrated | proprietary, a Sublime HQ product used under its license |
| [Visual Studio](https://visualstudio.microsoft.com) | `praetorctl editors generate` writes the `.clang-tidy` its code analysis reads | integration, integrated | proprietary, a Microsoft product used under its license terms |
| [Claude Code](https://github.com/anthropics/claude-code) | Agent client praetor compiles agent context for and configures | integration, integrated | proprietary, © Anthropic PBC; used under Anthropic's Commercial Terms of Service |
| [Codex CLI](https://github.com/openai/codex) | Agent client praetor compiles agent context for and configures | integration, integrated | Apache-2.0 |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | Agent client praetor compiles agent context for and configures | integration, integrated | Apache-2.0 |
| [Antigravity](https://antigravity.google) | Agent client praetor compiles agent context for and configures | integration, integrated | proprietary, a Google product used under Google's terms |
| [Cline](https://github.com/cline/cline) | Agent client praetor compiles agent context for and configures | integration, integrated | Apache-2.0 |
| [Continue](https://github.com/continuedev/continue) | Agent client praetor compiles agent context for and configures | integration, integrated | Apache-2.0 |
| [Kilo Code](https://github.com/Kilo-Org/kilocode) | Agent client praetor compiles agent context for and configures | integration, integrated | MIT |
| [OpenCode](https://github.com/anomalyco/opencode) | Agent client praetor compiles agent context for and configures | integration, integrated | MIT |
| [Cursor](https://cursor.com) | Agent client praetor compiles agent context for and configures | integration, integrated | proprietary, a product of Anysphere used under its terms |
| [Windsurf](https://windsurf.com) | Agent client praetor compiles agent context for and configures | integration, integrated | proprietary, used under its provider's terms |
| [GitHub Copilot](https://github.com/features/copilot) | Agent client praetor compiles agent context for and configures | integration, integrated | proprietary, a GitHub service used under GitHub's terms |

## Specifications and standards

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [The Power of 10 rules](https://spinroot.com/gerard/pdf/P10.pdf) (Gerard J. Holzmann, NASA JPL; IEEE Computer 39(6):95–97, 2006, [doi:10.1109/MC.2006.212](https://doi.org/10.1109/MC.2006.212)) | The basis of the HISS invariants | specification, inspired | none |
| [Semantic Versioning 2.0.0](https://semver.org) | Version comparison and release tags | specification, integrated | CC-BY-3.0 |
| [Conventional Commits 1.0.0](https://www.conventionalcommits.org) | Commit subjects and the commit check | specification, integrated | MIT, © 2018 Conventional Changelog |
| [Keep a Changelog 1.1.0](https://keepachangelog.com) | Changelog sections | specification, integrated | MIT, © 2014 Olivier Lacan |
| [Developer Certificate of Origin 1.1](https://developercertificate.org) | The `Signed-off-by` requirement | specification, integrated | none, © 2004, 2006 The Linux Foundation and its contributors; everyone may copy it verbatim |
| [SLSA v1.0](https://slsa.dev) | Release provenance | specification, integrated | Community-Spec-1.0 |
| [in-toto Attestation Framework v1](https://github.com/in-toto/attestation) | The provenance statement format | specification, integrated | Apache-2.0 |
| [CycloneDX](https://cyclonedx.org) | The SBOM format `praetorctl sbom` writes | specification, integrated | Apache-2.0 |
| [SPDX](https://spdx.org) | License identifiers and the SBOM format Syft writes during a release | specification, integrated | Community-Spec-1.0, older portions CC BY 3.0 |
| [Model Context Protocol](https://modelcontextprotocol.io) | The MCP servers | specification, integrated | Apache-2.0, moving from MIT; parts not yet relicensed stay MIT |
| [Schema.org](https://schema.org) | JSON-LD in the documentation presets | specification, integrated | CC-BY-SA-3.0 |
| [EditorConfig](https://editorconfig.org) | Editor settings the adoption writes | specification, integrated | BSD-2-Clause, © 2019 EditorConfig Team |
| [AGENTS.md](https://agents.md/) | The canonical agent instruction file | specification, integrated | MIT, © 2025 OpenAI |
| [SARIF 2.1.0](https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html) | The static-analysis result format whose compiler and linter logs praetor distills (`internal/lockdown`) | specification, integrated | LicenseRef-OASIS-IPR-Policy, © OASIS Open; published under the OASIS IPR Policy in its RF on RAND terms mode |
| [C4 model](https://c4model.com) | The levels of the architecture views (`docs/architecture/c4-models.md`) | specification, inspired | CC-BY-4.0, Simon Brown |
| [OSV schema](https://github.com/ossf/osv-schema) | The vulnerability entry format the Go vulnerability gate reads from govulncheck's stream (`internal/govuln`) | specification, integrated | Apache-2.0 |

## Fonts and icons

Material for MkDocs bundles these icon sets and loads these fonts from Google Fonts for this site
and for the MkDocs preset. Neither site sets `theme.font: false`, so a visitor's browser fetches
the fonts from Google.

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [Material Design Icons](https://pictogrammers.com/library/mdi/) | The theme toggle icons of this site and of the MkDocs preset (`material/` icons in `mkdocs.yml`), bundled by Material for MkDocs | font or icon, integrated | LicenseRef-Pictogrammers-Free-License AND Apache-2.0, Pictogrammers; some icons are redistributed under Apache-2.0, as the upstream license states |
| [Font Awesome Free](https://fontawesome.com) | The GitHub icon in this site's footer (`fontawesome/brands/github` in `mkdocs.yml`), bundled by Material for MkDocs | font or icon, integrated | CC-BY-4.0, for the icons; Font Awesome Free fonts are OFL-1.1 and its code MIT |
| [Roboto](https://fonts.google.com/specimen/Roboto) | Body font of this site and of the MkDocs preset: Material for MkDocs loads it from Google Fonts unless `theme.font` is false | font or icon, integrated | OFL-1.1, Christian Robertson, ParaType, Font Bureau |
| [Roboto Mono](https://fonts.google.com/specimen/Roboto+Mono) | Code font of this site and of the MkDocs preset, loaded the same way | font or icon, integrated | OFL-1.1, Christian Robertson |

## Build and CI tooling

These run in the build, the hooks or CI, and the download declarations name what CI fetches
outside a manifest. None of them ships in the binaries.

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [Lefthook](https://github.com/evilmartians/lefthook) | Git hooks; CI installs it with go install | tool, used by CI | MIT |
| [GoReleaser](https://goreleaser.com) | Release archives, the container image and checksums | tool, used by CI | MIT |
| [golangci-lint](https://golangci-lint.run) | Go linting, run with go run at its latest release | tool, used by CI | GPL-3.0-only, the upstream LICENSE is the GPL version 3 text, with no notice granting later versions |
| [gosec](https://github.com/securego/gosec) | Go security scan, a tool of `tools/go/go.mod` | tool, used by CI | Apache-2.0 |
| [Gitleaks](https://github.com/gitleaks/gitleaks) | Secret scan, a tool of `tools/go/go.mod` | tool, used by CI | MIT |
| [govulncheck](https://go.googlesource.com/vuln) | Go vulnerability scan, a tool of `tools/go/go.mod` | tool, used by CI | BSD-3-Clause, © The Go Authors |
| [gofumpt](https://github.com/mvdan/gofumpt) | Format-checks the Go managed assets adoption copies, a tool of `tools/go/go.mod` | tool, used by CI | BSD-3-Clause, © 2019, Daniel Martí |
| [Semgrep](https://semgrep.dev) | Static analysis rules | tool, used by CI | LGPL-2.1-or-later, the license expression of the PyPI release |
| [Black](https://github.com/psf/black) | Formats the hook sources adoption copies (`.config/hook-lint/requirements.in`) | tool, used by CI | MIT |
| [Flake8](https://github.com/PyCQA/flake8) | Lints the hook sources adoption copies | tool, used by CI | MIT |
| [Ruff](https://github.com/astral-sh/ruff) | Lints and format-checks the Python managed assets adoption copies | tool, used by CI | MIT |
| [yamllint](https://github.com/adrienverge/yamllint) | Lints the YAML praetor emits and the portability job's YAML | tool, used by CI | GPL-3.0-or-later |
| [ShellCheck](https://github.com/koalaman/shellcheck) | Lints the shell hooks; the portability job downloads a pinned release | tool, used by CI | GPL-3.0-or-later, the notice of every upstream source file |
| [Cosign](https://github.com/sigstore/cosign) | Keyless signing and verification | tool, used by CI | Apache-2.0 |
| [Syft](https://github.com/anchore/syft) | SBOM generation | tool, used by CI | Apache-2.0 |
| [Helm](https://github.com/helm/helm) | Lints, packages and pushes the chart in `deploy/helm/praetor` during a release | tool, used by CI | Apache-2.0 |
| [Renovate](https://github.com/renovatebot/renovate) | Dependency update proposals; `renovate.json` extends its built-in presets `config:recommended`, `:dependencyDashboard`, `:semanticCommits` and `:maintainLockFilesWeekly`, and CI installs it to validate the configuration | tool, used by CI | AGPL-3.0-only |
| [REUSE](https://reuse.software) | License compliance check, run by the reuse-action | tool, used by CI | GPL-3.0-or-later |
| [Chromium](https://www.chromium.org) | The browser Playwright downloads in CI to render and check the documentation figures | tool, used by CI | BSD-3-Clause, © 2015 The Chromium Authors |

## Development dependencies

Development dependencies of the figure tooling (`tools/figures/package.json`) and the VS Code
extension (`editors/vscode/package.json`), and the packages the vendored interfig manifest names.

| Project | Use | Kind and relation | License |
| :-- | :-- | :-- | :-- |
| [jsonschema](https://github.com/santhosh-tekuri/jsonschema) (`v6.0.3`) | The JSON Schema oracle of the test-only module `tools/schemacheck`; the production module does not link it | dependency, used by CI | Apache-2.0 |
| [esbuild](https://github.com/evanw/esbuild) | Bundles the figure player from `tools/figures/package-lock.json` | tool, used by CI | MIT |
| [Playwright](https://github.com/microsoft/playwright) | Drives the browser of the figure checks; the vendored interfig manifest names it as well | tool, used by CI | Apache-2.0 |
| [Sätteri](https://github.com/bruits/satteri) (`satteri`) | Markdown and MDX processing of the figures' Astro integration (`tools/figures/astro.mjs`) and its tests | dependency, used by CI | MIT |
| [DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped) | Type declarations (`@types/react`, `@types/react-dom`, `@types/node`, `@types/vscode`) for the figure tooling and the VS Code extension | dependency, used by CI | MIT |
| [vscode-languageclient](https://github.com/microsoft/vscode-languageserver-node) | The language client of the praetor VS Code extension (`editors/vscode`) | dependency, integrated | MIT |

<!-- REUSE-IgnoreEnd -->
