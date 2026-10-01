# Archetype and Facet Authoring Guide

Learn how to define new composable profiles and cross-cutting security/operational facets in the catalog Praetor ships, in this repository or in your fork of it.

```figure
lattice-join
```

---

## 0. The shipped catalog

Fourteen profiles ship in `.config/archetypes/`:

`app-service` · `closed-private` · `container-image` · `framework` · `gitops-infra` ·
`library-client` · `native-gpu-systems` · `org-health` · `os-image` · `pages-site` ·
`planning-artifacts` · `template-seed` · `upstream-fork` · `web-package`

Six facets ship in `.config/archetypes/facets/`.

**Every catalog file passes yamllint's default rules.** Adoption copies the pinned files byte for
byte into an adopter's `.config/archetypes`, where the adopter's own lint may run, so each file
opens with the `---` document start and keeps every line within 80 columns. Wrap a long quoted
`description` across lines at single spaces, which YAML folds back into one space. `make
hooks-lint` lints the whole catalog (`scripts/test_emitted_yaml_lint.py`).

**Changing a file moves its digest.** Re-pin `.standards.lock` with the digests `praetorctl
audit` reports, as the lock's header describes. An adopter's lock pins the earlier text, and a
re-run of `praetorctl adopt` against the new catalog fails on it until `--force`. The exception
is a layout-only change listed in `priorCatalogDigests` (`internal/adopt/policy_catalog.go`):
a lock that pins only those texts is re-pinned without `--force`
(`TestAdoptRepinsAnUnmodifiedEarlierCatalog`), and only while the new catalog decodes to
exactly the values of each earlier text (`isLayoutOnlySuccessor`). Once a later change moves
a value, adopters still on an earlier text need `--force` again
(`TestAdoptDoesNotRepinAnEarlierCatalogToChangedValues`).

**A profile is not a flavor.** A profile says what governance applies; a flavor says which templates,
settings and toolchains a repository of that kind requires. Five profiles currently have any flavor
implementing them — `app-service`, `framework`, `native-gpu-systems`, `container-image` and
`os-image`. For the other nine, `flavor audit`, `flavor apply` and adoption report **not applicable**
rather than measuring or scaffolding the repository against an inferred language flavor. A flavor
is only ever chosen among the flavors of the repository's profile (`internal/flavor/resolve.go`).

### Flavor templates: one embedded body, checked content

Every template a flavor requires states where its content comes from, in
`internal/flavor/definitions.go`, exactly one way:

| Field | Meaning | Example |
| :--- | :--- | :--- |
| `Source` | a body under `templates/`, compiled into the binary and rendered by `praetorctl flavor apply` | `go/ci-go.yml.tmpl` for `.github/workflows/ci.yml` |
| `Producer` | the command that writes the file; `flavor apply` never writes it, `--force` included, and lists it under *Left to Producer* | `praetorctl adopt` for `.standards.yaml`, `praetorctl compile-context` for `CLAUDE.md` |

A `Source` template may also carry `Resolve`, which reads what the body needs from the repository:
the facts it renders against (fields of `templates.Context`) and anything missing for it to work as
written. Where it reports something missing, `flavor apply` writes nothing, `--force` included, and
lists the path and what is missing under *Unmet Requirement*; `praetorctl adopt` turns each into a
warning. The audit still requires the file.

A template whose body changed may carry `Prior`: the digests (`util.CanonicalTextDigest`) of the
texts earlier releases scaffolded at its path. Without `--force`, `flavor apply`, and so a plain
`praetorctl adopt`, refreshes a file that holds one of them in one consistent line-ending style to
the current rendering, keeping that style. It lists the file under *Refreshed Earlier Praetor Text*
(`refreshed_templates` in the apply report), and adoption records it as reconciled. An edited copy
matches no digest and stays until `--force`, and the manifest, the lock and the ledger are never
refreshed. While `Resolve` withholds the body, a file already there is kept. An earlier text stays
unrefreshed and is listed under *Unmet Requirement* with the reason, and any other file is reported
skipped (`internal/flavor/target_write_internal_test.go`). Every recorded text needs a fixture
that reproduces its digest, as `TestRustfmtPriorTextsAreEarlierRenderings`
(`internal/flavor/rustfmt_test.go`) holds for `rustfmt.toml`.

A template with neither is an apply error, not a placeholder. `flavor apply` used to write a one-line
`# <file> configuration for <owner>/<repo>` comment for every template it had no body for, which
disabled every built-in gitleaks rule (#410) and scaffolded workflows that ran nothing.
`praetorctl flavor inspect <flavor>` prints the producer beside each producer-owned template.

### Flavor settings: the ruleset is rendered, the rest are deferred

A flavor's `RequiredSettings` (`SettingItem` in `internal/flavor/flavor.go`) take one of two routes
through `flavor apply` (`internal/flavor/settings_apply.go`):

| Setting | What `flavor apply` does | Test |
| :--- | :--- | :--- |
| `.github/rulesets/main.json` | renders it through `forge.RenderRulesetForRepository`, the renderer adoption's `branch-ruleset` step uses, under the effective policy (`config.ResolveRepositoryPolicy`, or the built-in default without `.standards.yaml`) | `TestApplyFlavor_Positive_EffectivePolicyDecidesTheRuleset` |
| a setting with `Producer` | writes nothing and lists it as `deferred` with the command that writes it: `praetorctl adopt` for `lefthook.yml`, `praetorctl editors generate` for `.vscode/settings.json` | `TestApplyFlavor_Positive_RulesetRequiresTheScaffoldedWorkflows` |

- Settings run after templates, so the ruleset requires the status checks of the workflows apply
  has just written.
- A ruleset that already is the rendering, line endings aside, is reported `unchanged`: the text
  comparison adoption applies to the same file. The rendering that was current before this apply
  added workflows (`forge.PriorRulesetDigests`), such as the one adoption wrote, is `refreshed`
  without `--force`, in its own line-ending style
  (`TestApplyFlavor_Positive_RefreshesTheRulesetCurrentBeforeApply`).
- Any other ruleset that differs is `kept` and reported, and `--force` replaces it. That includes a
  rendering with a single value edited, such as a signature rule, a review count or an added status
  check (`TestApplyFlavor_Negative_ValueEditedRulesetIsKeptWithoutForce`,
  `TestApplyFlavor_Negative_EditedRulesetIsKeptWithoutForce`). Adoption decides the same way
  ([refreshing a ruleset](../adoption.md#refreshing-a-ruleset-praetor-rendered-earlier)).
- `adoption.decline: [branch-ruleset]` in `.standards.yaml` stops `flavor apply` too. The decline is
  read through `adopt.RepositoryArtifactDeclined` (`TestFlavorApply_Negative_HonoursAdoptionDecline`).
- A policy that does not resolve fails the ruleset alone. The templates are still applied.
- A setting with neither a renderer nor a producer is an apply error.
  `TestRequiredSettings_Positive_EveryBuiltinSettingHasARendererOrProducer` requires one of the two
  for every built-in flavor.
- `praetorctl adopt` applies the flavor with `ApplyOptions.TemplatesOnly`, because its own
  `branch-ruleset` step writes the ruleset once every workflow of the run exists.

`flavor apply` alone makes the flavor audit pass only for a flavor whose other required settings are
already in place. The ruleset is the one setting it renders. `lefthook.yml` and
`.vscode/settings.json` come from their producers, and `flavor apply` does not render them.
`TestApplyFlavor_Positive_FreshRepositoryGetsTheRulesetAndPassesTheAudit` holds the audit to a pass
after `flavor apply` alone for `native-gpu-systems` and `infra-k8s`. For any other flavor, run the
producer the report names; `flavor inspect` prints it next to each deferred setting.

**The body is the file.** `templates/embed.go` embeds `templates/*/*.tmpl`, so the file under
`templates/` is byte for byte what an adopter receives. Actions use `<%` and `%>` rather than `{{ }}`,
because the workflows carry GitHub expressions such as `${{ runner.os }}`; a maintainer note goes in a
template comment, `<%- /* note */ -%>`, which renders to nothing. The context a body can name is
`templates.Context`; a body naming anything else fails `TestEveryShippedTemplateRenders`
(`templates/embed_test.go`). `flavor apply` fills `Owner` and `RepoName` from the origin remote
only; without one, `Owner` is empty and `RepoName` is the checkout directory's name, so a body that
names the repository guards the owner, as `templates/native/.gitleaks.toml.tmpl` does
(`TestApplyFlavor_Negative_CheckoutLayoutIsNotOwner`). `.clang-tidy` is shared with the Visual Studio editor target
(`internal/editor/editor.go`), so both commands write one configuration setting `WarningsAsErrors: '*'` under HISS-10.

**The audit reads the content.** `flavor audit` counts a template only when it is a regular file —
not a directory, and a symbolic link only when it resolves inside the repository — whose content
passes the template's `Validator` (`internal/flavor/template_validators.go`). Settings share the same
reader. Each validator checks what its format makes checkable and no more:

| Validator | Accepts |
| :--- | :--- |
| `validWorkflow` | a workflow with a non-empty `jobs` mapping |
| `validClangTidyConfig` | a YAML mapping declaring a non-empty `WarningsAsErrors` setting |
| `validDockerfile` | at least one `FROM` instruction |
| `validGitleaksConfig` | a config that loads rules: `[extend] useDefault = true`, an `[extend] path`, or `[[rules]]` |
| `assignsTOMLKey` | TOML assigning at least one key |
| `validXMLDocument` | well-formed XML with an element |
| `validMarkdownDocument` | text beyond headings and HTML comments |
| `validYAMLMapping`, `validJSONObject` | a non-empty mapping or object, as for settings |
| `carriesCode` | a line that is not a comment, for JavaScript, TypeScript and `tsconfig.json`, which is JSON with comments |

`flavor apply` asks a narrower question than the audit. It writes nothing under the canonical name
while any `AltPaths` file exists, even one the audit rejects (a comment-only `tsconfig.base.json`, or a
link to a config outside the repository): that file is the one the toolchain reads, and a scaffolded
rival beside it would contradict it. The audit keeps reporting the template missing until the
alternative's content passes. `flavor apply` lists the file it kept under `Kept Existing Config`, and
adoption records it against that file (`covered_templates` in the apply report). `--force` is the
exception: it writes the canonical file beside the alternative, so use it only when you mean to
replace the alternative, and then delete the old file.

A template whose tool searches several names in a fixed order and reads only the first one it finds
declares `Search` (the tool and its names, in its order) instead of `AltPaths`. Every name satisfies
the template, but the audit validates only the first one present, because the tool never reads a
later one, and lists the later ones under `Shadowed Templates (advisory)` with the file the tool
reads. `Path` must be one of the names; `TestEverySearchedTemplateScaffoldsASearchedName`
(`internal/flavor/template_alternatives_test.go`) fails otherwise.

**Scaffolded workflows and images must run as written.** A workflow step may call only what the job
installs: the Go CI job runs `go vet ./...` and `go test -race ./...`, not `make verify-all`, whose
recipes call `praetorctl`, which no step installs. Praetor's own gates run from the git hooks adoption
writes. `TestScaffoldedWorkflowsRunWithoutAPraetorBinary` (`internal/flavor/emitted_content_test.go`)
fails on a step naming the praetor binary. The Go `Dockerfile` builds the module's only main package,
wherever it lives; with none or several, `docker build` stops and names them, and
`--build-arg MAIN_PACKAGE=./cmd/<name>` picks one. `TestScaffoldedDockerfileBuilderCompilesTheModulesMainPackage`
(`internal/flavor/dockerfile_build_test.go`) executes the builder instruction against each layout.
The JVM CI job (`templates/jvm/ci-jvm.yml.tmpl`) runs whichever build the repository carries: the
Maven wrapper, `mvn`, the Gradle wrapper, then the runner's `gradle` for a Gradle build with no
wrapper. A wrapper committed without its executable bit runs through `sh`. A Gradle build with no
wrapper on a runner without `gradle` stops with a message naming the missing wrapper.
`TestJVMBuildStepRunsTheRepositorysBuild` (`internal/flavor/jvm_ci_test.go`) executes the step
against each layout with stub build tools. The Flutter analyzer config
(`templates/flutter/analysis_options.yaml.tmpl`) includes `package:flutter_lints/flutter.yaml` or
`package:lints/recommended.yaml` only when `pubspec.yaml` declares that package as a dependency or dev
dependency (`internal/flavor/dart_lints.go`). Otherwise it has no include, because `flutter analyze`
fails on an include pub cannot resolve, and keeps its core linter rules, which need no package.
`TestScaffoldedDartAnalysisConfigIncludesOnlyADeclaredLintPackage` (`internal/flavor/dart_lints_test.go`)
covers each case.

The Rust formatter config (`templates/rust/rustfmt.toml.tmpl`) declares the edition every crate of
the root `Cargo.toml`'s workspace is on (`internal/flavor/rustfmt.go`). `cargo fmt` passes each
crate's edition to rustfmt, but rustfmt run directly, as a hook on staged files does, reads it from
`rustfmt.toml`, so any other edition makes the two disagree (#567). The crates are the root package
and every `[workspace]` member: a listed path, or each directory a `*` or `?` pattern matches that
no `exclude` entry names or contains. A crate inheriting its edition (`edition.workspace = true`)
takes the one `[workspace.package]` declares, and a workspace with no crate to read takes that one
directly. Where no crate declares an edition, the config has none either, since Cargo and rustfmt
then both use 2015. The crate editions are unknown when the repository has no root `Cargo.toml` (a
crate in a subdirectory), the root manifest cannot be read (larger than 1 MiB, not a regular file),
or a member cannot be read (no `Cargo.toml`, no `[package]`, a `**` or `[...]` pattern, a pattern
matching nothing, which `cargo metadata` also rejects, more than 256 members). There, and where the
crates share no edition, no single edition is known to agree with `cargo fmt` on every crate:
`flavor apply` writes no config and names the reason under *Unmet Requirement*. It keeps a
`rustfmt.toml` already there; an earlier Praetor text stays unrefreshed, with the reason listed
beside it. Crates that only path dependencies pull into the workspace are not read.
`TestScaffoldedRustfmtFollowsTheCrateEdition`,
`TestRustfmtApply_Negative_NoCommonEditionWithholdsTheScaffold` and
`TestRustfmtApply_Boundary_NoCommonEditionKeepsAnEarlierScaffold`
(`internal/flavor/rustfmt_test.go`) cover each layout.

A body that depends on the repository declares `Resolve`. The Node CI job installs from the committed
lockfile and runs the `test` script, and `typescript-node` matches any `package.json` in an
`app-service` repository and can be applied by name to any other. So `flavor apply` writes the job
only when all of these hold (`internal/flavor/node_ci.go`):

- The package manager is known: the one `packageManager` names (`npm`, `pnpm`, `yarn` or `bun`, as
  `<name>@<version>`), or, without a declaration, the one whose lockfile CI's checkout holds.
  Undeclared lockfiles of two managers leave the choice to you: name one in `packageManager`.
- CI's checkout will hold that manager's lockfile at the root: `package-lock.json` (npm 12 reads no
  `npm-shrinkwrap.json`), `pnpm-lock.yaml`, `yarn.lock`, or `bun.lock` (`bun.lockb` before Bun 1.2).
  The file on disk is not enough: a library that lists its lockfile in `.gitignore` still gets one
  from a local install, and CI never sees it. Git must track the lockfile, or would commit it because
  no ignore rule excludes it. A lockfile force-added past such a rule counts, because it is tracked.
  Outside a Git work tree, or without `git`, nothing shows what CI checks out, so the job is withheld.
- The `test` script is neither missing, blank nor the placeholder `npm init` writes.

The job then installs with that manager and fails when `package.json` disagrees with the lockfile:

| Manager | Set up by | Install | Scripts |
| :--- | :--- | :--- | :--- |
| npm | `actions/setup-node` with the npm cache | `npm ci` | `npm run lint --if-present`, the same for `build`, `npm test` |
| pnpm | Corepack | `pnpm install --frozen-lockfile` | `pnpm run --if-present lint`, the same for `build`, `pnpm run test` |
| Yarn | Corepack | `yarn install --immutable` on Yarn 2+, `--frozen-lockfile` on Yarn 1 | `yarn run lint` and `yarn run build` where `package.json` defines them when the job is scaffolded (Yarn has no `--if-present`), `yarn run test` |
| Bun | `oven-sh/setup-bun` | `bun install --frozen-lockfile` | `bun run --if-present lint`, the same for `build`, `bun run test` |

Corepack is installed from npm, because Node 25 and later ship without it. Corepack and `setup-bun`
install the version `packageManager` pins, or their own default. Yarn is 2 or later when the declared
version says so or, without a declaration, when `.yarnrc.yml` sets `yarnPath`, which Yarn 1 hands the
command over to.

These checks cover what the steps need, not whether your scripts pass. Elsewhere adoption requires no
Node check, and you write the CI job your repository needs. The `packageManager` parse and the
test-script decision are shared with adoption's verification plan (`internal/nodemanifest/scripts.go`),
which generates `npm run` commands for npm projects only.
`TestNodeCIJobIsScaffoldedOnlyWhereItRunsAsWritten` (`internal/flavor/node_ci_test.go`) checks every
action, tool, lockfile and script the scaffolded body names against the files `git add -A` stages in
each fixture, which is what CI's checkout carries.

**Adoption scaffolds the flavor of the profile it records.** `praetorctl adopt` resolves the flavor
under the profile it writes into `.standards.yaml` (`flavor.ResolveForProfile`), before it derives
the branch ruleset, so the scaffolded CI job is a required check from the first run and is the flavor
`flavor audit` measures afterwards. A Go service whose `package.json` only holds commit tooling is
adopted as `framework` and scaffolded as `go-service`; adoption used to scaffold `typescript-node`
there, because it detected across the whole catalog. A profile with no flavor, or whose flavors all
fail to match, gets no flavor templates and a warning naming the profile and
`praetorctl flavor apply --flavor=<name>` (`internal/adopt/flavor_scaffold_test.go`). Adoption once
applied `go-library` there, and its CI job (`setup-go` against a `go.mod` the repository lacks)
became a required check no pull request could pass.

When you add a template, give it a `Source` (add the body under `templates/<ecosystem>/`) or a
`Producer`, and a `Validator`. `TestEveryRequiredTemplateStatesItsContent`
(`internal/flavor/template_content_test.go`) fails otherwise, and also fails when the scaffolded body
does not pass its own validator or the old comment placeholder does.

### The `os-image` flavor: a forge is what it builds

`os-image` is the first profile whose flavor is not a language stack. It requires `shellcheck` and
`yamllint`, plus a yamllint policy for the image and workflow definitions, because an image forge is
audited on the pipeline that produces a bootable artifact rather than on a compiler toolchain.

**A build engine only where its input is.** `packer` is asked for only in a repository holding a
Packer template (`ToolchainItem.Markers` in `internal/flavor/flavor.go`): a kernel forge or an mkosi
image builds without it, and an item it cannot use is neither counted nor reported missing.
`praetorctl flavor inspect os-image` prints the limit as `only where: packer/*.pkr.hcl`. A limit may
name only the flavor's own detection markers (`TestOSImageToolchainMarkersAreForgeMarkers` in
`internal/flavor/os_image_test.go`). The toolchain check is advisory and never scored.

**Any yamllint configuration name counts.** yamllint reads the first of `.yamllint`,
`.yamllint.yaml` and `.yamllint.yml` it finds (`find_project_config_filepath` in yamllint's `cli.py`,
checked on 1.38.0), so the template searches that list (`yamllintConfigNames` in
`internal/flavor/definitions.go`). The audit accepts each name and judges the one yamllint reads: an
empty `.yamllint` beside a valid `.yamllint.yml` fails, because yamllint never reads the valid file.
With two present, the audit passes and names the file in use under `Shadowed Templates (advisory)`.
`flavor apply` and adoption scaffold `.yamllint.yml` only for a repository with none of the three
(`internal/flavor/yamllint_config_test.go`, `internal/adopt/flavor_report_test.go`).

**What marks a forge.** Four markers, stated once in the `os-image` rule of the classification
table in `internal/classify/classify.go`. `OSImageFlavor.Detect` (`internal/flavor/definitions.go`)
reads that rule through `classify.HasMarkerOf` instead of keeping its own copy, so the profile a
checkout classifies as and the flavor it resolves to cannot disagree about what a forge is:

| Marker | Kind | What it identifies |
| :--- | :--- | :--- |
| `packer/*.pkr.hcl` | glob | a Packer template tree |
| `mkosi.conf` | fixed path | an mkosi image definition |
| `build/mkosi.conf` | fixed path | the same, under a build directory |
| `kconfig/[^.]*.config` | glob | Kconfig fragments of a kernel forge |

The archetype names kernels, initramfs and UKIs beside disk images (`.config/archetypes/os-image.yaml`),
and a forge that builds a kernel keeps its Kconfig fragments under `kconfig/` with no Packer template
or `mkosi.conf` (#615). The `[^.]` keeps hidden files out: Go's `*` matches a leading dot, and
`kconfig/.config` is the configuration Kconfig writes, not a fragment. A root `.config` or an empty
`kconfig/` does not mark a forge either (`internal/classify/os_image_test.go`,
`internal/gating/flavor_stage_test.go`).

The profile promises signed outputs, and `praetorctl provenance` refuses to attest a `.deb` that
is not a Debian package, an `.efi` that is not an EFI image, or a Unified Kernel Image without its
`.linux` section, so a placeholder a build left behind fails before it is signed
([content checks](releasing.md#content-checks)). A UKI named like a kernel, such as
`vmlinuz-7.2.4.efi`, is declared with `--uki` ([declaring a UKI](releasing.md#declaring-a-uki)).

**The glob marker rule.** Most markers are a fixed path: `go.mod` either exists or it does not. A
Packer tree cannot be written that way, because what identifies the forge is holding *some*
template, not a particular one. `util.MarkerExists` is the single matcher for both tables: a fixed
marker stays a `stat`, a pattern containing `*`, `?` or `[` expands with a bounded scan
(`maxMarkerMatches = 256`, HISS-02) and counts **only regular files**. That last part is what keeps
the negatives honest — an empty `packer/`, a `packer/` holding only a `README.md`, and a *directory*
named `x.pkr.hcl` are all non-matches, so no repository becomes an image forge by owning a folder. A
malformed pattern matches nothing rather than erroring: a marker table describes the world, it is
not user input to validate.

**Why it outranks `go.mod` and `pyproject.toml`.** `OSImageFlavor` is registered ahead of the
language flavors, and its markers sit ahead of `go.mod` and `pyproject.toml` in `classify.rules()`.
An image forge carries both — a Go CLI that drives the build, a Python suite that verifies the
result — so whichever language flavor claimed it first would describe the *tooling* instead of the
*product*. That is not hypothetical: an adopter's OS image forge was audited as a Go service and told to add
a Dockerfile it has no use for. Order the markers the same way when you add a profile whose
repositories are known by their output rather than their source language.

### JavaScript flavors: flat ESLint config, never a second one

`typescript-node` and `frontend-svelte` require ESLint configuration through one shared definition in
`internal/flavor/eslint.go`:

- **What counts as configured.** Any file ESLint already loads: `eslint.config.{js,mjs,cjs,ts,mts,cts}`,
  or a legacy `.eslintrc`, `.eslintrc.{js,cjs,json,yaml,yml}`. A repository carrying one conforms, and
  `flavor apply` writes nothing beside it unless `--force` is passed.
- **What gets scaffolded.** Only for a repository with none of those: `eslint.config.mjs`
  (`templates/node/eslint.config.mjs.tmpl`), holding
  ESLint's recommended JavaScript rules in the form the `@eslint/js` README documents. It is `.mjs`
  because a `.js` file is ESM or CommonJS depending on `package.json` `"type"`, and `.mjs` loads in
  both kinds of repository.
- **What is never scaffolded.** Legacy eslintrc files. ESLint v10 cannot load them, which is why the
  shipped catalog already refuses to name them (`internal/config/shipped_catalog_test.go`).

Two guards in `internal/flavor/eslint_guard_test.go` hold every flavor to this. One fails if any flavor
requires a legacy eslintrc file. The other fails if any JavaScript or TypeScript template has no body
of its own or renders one starting with `#`, which is a syntax error in those languages.
`frontend-svelte`'s `playwright.config.ts` (`templates/svelte/playwright.config.ts.tmpl`) carries the configuration the
`@playwright/test` documentation shows, without a `baseURL` or `webServer`, since those depend on an
application server the flavor cannot know about.

Two profiles are worth reading before writing a new one, because their correctness looks like a
mistake:

- **`upstream-fork`** declares every complexity bound as `0` and disables linear history and signed
  commits. A contribution fork must match the upstream it submits to, so a gate that rewrites the
  tree makes every patch unmergeable. The looseness is the feature.
- **`org-health`** requires one approving reviewer rather than two despite its blast radius, because
  the repositories that need it are the least maintained ones and a two-approval rule on a
  repository nobody watches is how it goes stale.

## 1. Profile Definition Anatomy

Profiles represent the primary technology stack or architecture. Create `.config/archetypes/{profile-id}.yaml`.
The shipped `.config/archetypes/native-gpu-systems.yaml`, in full:

```yaml
---
id: "native-gpu-systems"
name: "Native GPU & Compute Systems"
description: "High-performance C/C++/Rust/CUDA/Vulkan systems with deterministic
  memory bounds and zero dynamic frame allocations"
runtime: "native"

complexity:
  max_cyclomatic: 10
  max_cognitive: 12
  max_func_loc: 75
  max_statements: 40

memory:
  zero_frame_malloc: true
  banned_alloc_in_ticks: true

branch_protection:
  enforce_linear_history: true
  require_signed_commits: true
  required_approving_reviewers: 2
  dismiss_stale_reviews: true

supply_chain:
  slsa_level: 3
  enforce_cosign: true
  require_sbom: true

linters:
  - "clang-tidy"
  - "clippy"
  - "semgrep"
  - "cppcheck"

devcontainer_features:
  - "ghcr.io/devcontainers/features/rust:1"
  - "ghcr.io/devcontainers/features/common-utils:2"
  - "ghcr.io/devcontainers/features/nix:1"
```

---

## 2. Facet Definition Anatomy

Facets are cross-cutting policy modifiers. Create a YAML file under `.config/archetypes/facets/`.
The shipped `.config/archetypes/facets/security-high.yaml`, in full:

```yaml
---
id: "security:high"
name: "High-Security Provenance & Hardening"
description: "SLSA Level 3 attestations, keyless Cosign signatures, SBOM
  generation, and non-root execution"

supply_chain:
  slsa_level: 3
  enforce_cosign: true
  require_sbom: true

branch_protection:
  enforce_linear_history: true
  require_signed_commits: true
  required_approving_reviewers: 2
  dismiss_stale_reviews: true

linters:
  - "gitleaks"
  - "trivy"

devcontainer_features:
  - "ghcr.io/devcontainers/features/common-utils:2"
```

**The `id` field is the facet's identity; the file name is descriptive.** `.standards.yaml`
`facets:` entries and `.standards.lock` resolve against the declared `id`, because the catalog
index keys every file by it (`indexArchetypesWithSnapshots` in `internal/config/archetype_index.go`).
A file name cannot repeat the id, since `:` is not legal in a Windows file name, and the shipped
names abbreviate (`api:public-contract` lives in `api-public.yaml`). For a new facet, replace the
colon with a hyphen. Two rules still bind the name:

- A file without an `id` falls back to its name minus `.yaml`, so always declare one.
  `TestFacetIndex_Boundary_ResolvesByDeclaredIDNotFileName` in
  `internal/config/shipped_facets_test.go` covers both cases.
- Adoption copies a facet under its file name into the adopter's catalog, and the index rejects two
  files declaring one id (`TestCatalogProjectionFindsAlternateFilenameCollisionWithoutWrites` in
  `internal/config/catalog_projection_test.go`). Renaming a shipped facet therefore collides with
  the copy an adopter already holds; keep shipped names stable.

**Keep facets language-neutral.** Any profile may select a facet, so a facet lists no tool that
reads only one language's source (`gocyclo`, `benchstat`, `oapi-codegen`): that tooling belongs to
the profile, which declares a `runtime`. `TestShippedFacets_Negative_NoGoOnlyLinter` enforces this
for the shipped catalog.

**Cite gated invariants as gated, and nothing else.** A description may cite a HISS invariant from
the `AGENTS.md` table as enforced. An invariant defined only in the extended spec, such as HISS-03
or HISS-14, is cited together with `docs/standards/hiss-spec.md`
(`TestShippedFacets_Negative_UngatedHISSCitesTheExtendedSpec`).

**Claim only what runs.** A facet's lattice dimensions are policy; its description names the
checks that enforce them. Two facets also enable managed asset families
(`Families` in `internal/managedasset/family.go`), files adoption writes while the facet is
declared, removes once it is not, and audit locks: `docs:seo-portal` the documentation gate and
`api:public-contract` the [Go API compatibility gate](api-compatibility.md). The latter's
description names that workflow and the `Migration:` footer check and nothing else; its `linters`
entries are names in the resolved policy, not commands any adopted repository runs.

---

## 3. The Strictness Lattice ("Highest Standard Wins")

When two profiles or facets define conflicting parameters, the monotonic supremum is calculated:
$$\mathcal{P}_{\text{resolved}} = \mathcal{P}_1 \sqcup \mathcal{P}_2 \sqcup \dots \sqcup \mathcal{F}_n$$

- Lower complexity limits win ($\min$).
- Greater security reviews and higher SLSA levels win ($\max$).
- Linters and container features form a deduplicated set union ($\cup$).

Built-in defaults are the first operand, so a profile or facet can only tighten them. Every
dimension below reaches the resolved policy (`config.Join` in `internal/config/config.go`,
tested by `TestLoadEffectivePolicyJoinsEveryProfileDimension` in
`internal/config/archetype_test.go`):

| Key | Join | Default |
| :-- | :-- | :-- |
| `complexity.*` | lowest positive limit; `0` means no bound | 15 / 20 / 60 / 75 |
| `branch_protection.enforce_linear_history`, `require_signed_commits`, `dismiss_stale_reviews` | `true` wins | `true`, `false`, `true` |
| `branch_protection.required_approving_reviewers` | maximum | 1 |
| `branch_protection.review_mode` | no catalog layer may set it (`TestLoadEffectivePolicyRejectsInvalidArchetypes`), so the join keeps `independent`; only the repository override may relax it to `single_maintainer`, after the join | `independent` |
| `supply_chain.slsa_level` | maximum | 1 |
| `supply_chain.enforce_cosign`, `require_sbom` | `true` wins | `false` |
| `memory.zero_frame_malloc`, `banned_alloc_in_ticks` | `true` wins (ZeroFrameMalloc over StandardHeap) | `false` |
| `error_unwraps` | `strict_ban` wins over `allow_with_comment` | `allow_with_comment` |
| `linters` | deduplicated union, first occurrence order | `govet` |
| `devcontainer_features` | deduplicated union by reference, first occurrence order; see the exception below | `common-utils` |

Two archetypes that declare the same value tie on it; the result is that value whichever is
pinned first (`TestResolvePolicyTiedArchetypesOnMemoryAndErrorUnwraps`).

DevContainer features are the one dimension that can fail instead of joining. When a consumer
resolves them (`ResolveDevContainerFeatures` in `internal/config/devcontainer_features.go`), it
re-reads the pinned profile and facet files and keys each feature by its identity, the reference
without its tag or digest. The same reference with the same options joins once. One identity with a
different tag, digest or options is ambiguous and fails closed (`mergeDevContainerFeature`, tested by
`TestResolveDevContainerFeaturesRejectsConflictsAndMalformedEntries` in
`internal/config/devcontainer_features_test.go`).

### The schema is closed

An archetype accepts exactly the keys `id`, `name`, `description`, `runtime`, `complexity`,
`memory`, `error_unwraps`, `branch_protection`, `supply_chain`, `linters` and
`devcontainer_features`, and inside each section only its documented keys. A misspelled or
unknown key fails the file instead of contributing nothing. The catalog index decodes every
file in `.config/archetypes`, selected or not, so one bad file fails lock verification, `plan`,
`audit`, `sync` and `adopt` until it is fixed
(`TestCatalogIndexRejectsUnknownKeyInAnUnselectedArchetype`). Also rejected:

- `branch_protection.review_mode`: single-maintainer review is a repository-only relaxation, set
  in `.standards.yaml` overrides;
- negative `required_approving_reviewers` or `slsa_level`;
- an `error_unwraps` value other than `strict_ban` or `allow_with_comment`;
- an empty linter name, or one with surrounding whitespace or control characters.

A blank or missing `id` defaults to the file name without `.yaml`.
