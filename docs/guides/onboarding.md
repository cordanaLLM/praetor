# Repository Onboarding Guide

Onboard one of your existing repositories into declarative governance with Praetor.

```figure
onboarding-path
```

## 1. Quickstart

Prerequisites: `praetorctl` installed ([workstation install](workstation-update.md)); a Praetor
checkout or source bundle, written `/path/to/praetor` below, which `--lock-source-root` names so
the lock can pin your profile and facets to content digests; a Git repository with your
files committed; a known hosting forge, meaning an `origin` remote on `github.com` or
`repository.forge` declared in `.standards.yaml` (any other host gets no Paperclip harness and the
audit fails on its absence; [Hosting forge](../adoption.md#what-adoption-reads-before-it-writes)). Run these in the
repository root. Each command exits 0 in a new Go module (a `go.mod` and one source file, a
`github.com` `origin` remote, committed):

```bash
# 1. Preview what adoption would write; the plan needs the lock source, as the real run does
praetorctl adopt --dry-run --lock-source-root=/path/to/praetor

# 2. Adopt: manifest, pinned lock and catalog, baseline, agent files, DevContainer, hooks, CI gates
praetorctl adopt --lock-source-root=/path/to/praetor

# 3. Stage what adoption wrote; audit reads tracked files
git add -A

# 4. Verify the configured governance contract
praetorctl audit --offline
```

One adopt run is the whole onboarding. It detects the profile (the report's `Archetype:` line);
when that is not the profile you want, pass `--profile` on the first run, or move an adopted
repository with `praetorctl profile set <profile> --lock-source-root=/path/to/praetor`, then refresh
with `praetorctl adopt --lock-source-root=/path/to/praetor` (add `--force` for the files `profile
set` lists; the plain run also refreshes the Paperclip harness, which `profile set` does not check)
([Changing the profile, facets or catalog](../adoption.md#changing-the-profile-facets-or-catalog)).
`--dry-run` needs `--lock-source-root` on a first adoption because the plan resolves the policy the
lock would pin; without it the dry run exits 1 with `new lock pins require an explicit verified
lock source root`. The files adoption writes are listed step by step in
[What Adoption Scaffolds Automatically](../adoption.md#what-adoption-scaffolds-automatically).
`--offline` makes the audit read nothing from the forge: the live Actions permission, workflow
run and branch-protection checks report as not made.

## Staged onboarding with `praetorctl init`

`praetorctl init` is the staged alternative. It requires canonical `AGENTS.md` instructions:
it compiles the agent files from it and writes none without it. It is not a step before
`adopt`, and it refuses once `.standards.yaml` exists (`ensureManifestAbsent` in
`cmd/standardsctl/init.go`), so do not run it after adoption.

```bash
# 1. Initialize configuration; pin the lock and write the catalog from the Praetor checkout
praetorctl init --lock-source-root=/path/to/praetor --profile framework --facets security:high,api:public-contract

# 2. Recompile the agent files and persona copies (rerun after every AGENTS.md edit)
praetorctl compile-context

# 3. Snapshot legacy technical debt infractions to prevent CI failure
praetorctl baseline --record --allow-increase --reason "<why>"

# 4. Prepare a portable devcontainer from reviewed Praetor sources
praetorctl devcontainer generate --source-root /path/to/praetor

# 5. Verify the configured governance contract
praetorctl audit --offline
```

Without `--lock-source-root`, init writes a placeholder lock with no content digests and prints a
warning saying so. `devcontainer generate` and `audit` then fail on the missing digest; pin the
lock afterwards with `praetorctl profile set --lock-source-root=/path/to/praetor`. With the
flag, init writes the lock adoption writes (`config.BuildLockfile`) and the pinned catalog under
`.config/archetypes/` (`adopt.MaterializePinnedCatalog`), and a fresh repository passes
`devcontainer generate` and the manifest, lock, digest, DevContainer, cross-agent context and
caveman register gates of the audit
(`TestInitLock_Positive_PinnedLockPassesGenerateAndAudit` in `cmd/standardsctl/init_lock_test.go`).
Init does not make the whole audit pass. The audit stops at its first failing gate, so an
init-only repository meets the gaps one at a time: no branch ruleset (`praetorctl sync` writes
it), with `docs:seo-portal` no documentation gate (missing `tools/markdownlint`), with
`api:public-contract` in a Go module no API compatibility gate (missing `tools/apicompat/gate`),
no HISS-11 supply-chain exception, no `.paperclip/harness.json`, no agent definitions and no
git hooks. Adoption writes all of them, which is why the quickstart uses it; `init` is for a
repository that fills those in by other means.

Step 1 writes your repository's identity into `.standards.yaml`. `repository.owner` and
`repository.name` come from the origin remote. Without a remote the owner is
`forge.default_owner` from your operator settings, selected with `--fleet-config`,
`--workstation-config` or `--manifest` (see
[framework, forge and topology](effective-policy.md#framework-forge-and-topology)), and the
name stays empty. Praetor ships no default owner: a field it cannot resolve is written empty
and reported as `[WARN] repository.owner not detected; set it in .standards.yaml` (or
`repository.name`), and you set it by hand (`initRepositoryIdentity` in
`cmd/standardsctl/init.go`; `TestInit_3D_RepositoryIdentity` in
`cmd/standardsctl/forge_owner_test.go`).

---

## Planning-only adoption through MCP

`standards_adopt` accepts `profile` and comma-separated `facets`, matching the
CLI's `--profile` and `--facets` selection. For example:

```json
{"path":"/workspace/project","source_root":"/workspace/praetor","profile":"planning-artifacts","facets":"agent:sandboxed","record_baseline":false,"dry_run":true}
```

Both paths must be allowed by the server's existing root policy. Inspect the
preview, then use the same selection with `dry_run: false` for an authorized
application. Omitted or empty selection retains the shared adoption defaults;
an empty facet string does not clear them. The adapter accepts at most 64
comma-separated entries and 8192 facet bytes, with a 128-byte profile bound.
The shared pinned catalog validates selected identities. Existing repository
configuration retains its normal preservation/force semantics.

The planning profile prepares governance without inventing Go or Rust manifests.
It does not qualify a native build, boot, release, running DevContainer or agent
activation. Those stages need their own selected checks and execution evidence.

## 2. Onboarding Workflow Stages

| Step | Action | Command | Expected Output |
| :--- | :--- | :--- | :--- |
| **1. Scaffolding** | Create declarative configuration and compile agent files | `praetorctl init --lock-source-root=/path/to/praetor` | `.standards.yaml`, a digest-pinned `.standards.lock`, `.config/archetypes/` and a zero-debt `.standards-baseline.json` created. The agent files are then written the way step 2 writes them (`initAgentContext` in `cmd/standardsctl/init.go`, `adopt.CompileAgentContext`): in a Git work tree the Praetor private-artifact block is merged into `.gitignore` unless Git already ignores `.workingdir/evidence/`; the text register is spliced into `AGENTS.md`; the six vendor files `CLAUDE.md`, `.cursor/rules/hiss-invariants.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md` and `.codex/rules.md` and the persona copies are compiled. An `AGENTS.md` the caveman lint rejects fails init after the files are written; fix it and run step 2 (`cmd/standardsctl/init_evidence_test.go`). |
| **2. Context Recompilation** | Recompile vendor files and persona copies | `praetorctl compile-context` | The vendor files rewritten from `AGENTS.md` (all six unless `agent_clients` in `.standards.yaml` selects fewer); each persona in `.agents/agents` copied to `.claude/agents`, `.github/agents`, `.gemini/agents` and `.codex/agents`. `praetorctl compile-context --verify` checks the same files and writes nothing. |
| **3. Brownfield Baselining** | Snapshot legacy debt | `praetorctl baseline --record --allow-increase --reason "<why>"` | `.standards-baseline.json` populated with existing debt. |
| **4. Devcontainer Setup** | Prepare a portable bootstrap | `praetorctl devcontainer generate --source-root /path/to/praetor` | JSON and exact source companions prepared; build and startup remain separate checks. |
| **5. Audit Verification** | Verify configured governance and debt-ratchet gates | `praetorctl audit` | After `init` alone the audit fails at the first gate init leaves open (see the gaps above); once those are filled every executed gate reports pass, and skipped or unsupported coverage remains explicit. |

---

## 3. Brownfield Technical Debt Ratcheting

Legacy infractions recorded in `.standards-baseline.json` will not fail CI status checks:

- **Monotonic Ratchet**: Technical debt must decrease over time ($V_{\text{total}}(t_1) \le V_{\text{total}}(t_0)$).
- **Touched-File Clean Rule**: Any legacy file modified during a pull request revokes previous exemptions and must be refactored clean. A rejection counts a touched file's findings as not in the baseline and baselined; both fail ([A HISS rejection names the violations](adoption-verification.md#a-hiss-rejection-names-the-violations)).
- **Stale Entries**: A cleanup that lands without `praetorctl baseline --record` leaves baseline entries that match nothing. `praetorctl audit` warns with their count; `--max-stale-baseline-entries=N` fails it past N ([A baseline looser than the tree is reported](adoption-verification.md#a-baseline-looser-than-the-tree-is-reported)).
- **Moved Code**: An entry is keyed by its rule, file and the function that holds the finding, not by its line, so lines inserted above a baselined function need no re-record. A renamed function or file does: `praetorctl baseline --record`, without `--allow-increase`, as the count is unchanged. A baseline recorded by an older engine keeps working and gains these keys at its next record ([A baseline entry survives a line shift](adoption-verification.md#a-baseline-entry-survives-a-line-shift)).
- **Initial Debt & Waivers**: Because `praetorctl init` seeds a clean baseline (0 infractions), recording existing debt in a brownfield repository represents an initial increase and requires an explicit, justified increase: `praetorctl baseline --record --allow-increase --reason "<why>"`. The same HISS-13 exception path applies to any subsequent unavoidable architectural exception that raises the recorded count. The reason is stored in the baseline alongside the raised count; there is no CLI command or file that mints a standalone signed waiver. The `hiss-waiver` repository label is a scaffolded taxonomy entry with no enforcement attached.
- **After a Praetor upgrade**: a newer build can add a check that finds debt in code nobody changed. The ratchet still refuses the higher count, but it no longer calls those findings introduced: when the scanned code is unchanged since the commit `commit_sha` names in `.standards-baseline.json`, or since the commit that last changed that file, the rejection tags them `(check added or changed since the baseline)`; a baseline without `commit_sha` gets the neutral `(not in the baseline)`. Either way the rejection names the remedy: fix the findings, or record them with `praetorctl baseline --record --allow-increase --reason "<why>"`. `praetorctl baseline --verify --all-violations` lists every failing finding first without rewriting the file ([A HISS rejection names the violations](adoption-verification.md#a-hiss-rejection-names-the-violations)).

---

## Flavors

A flavor names the stack of a repository within its profile and decides which templates, settings
and toolchains `flavor audit` expects. `praetorctl flavor list` prints the table below and
`praetorctl flavor inspect <name>` prints one flavor's required templates, settings and
toolchains. Detection tries the flavors in this order and stops at the first match
(`builtinFlavorList` in `internal/flavor/definitions.go`); rows and order are checked against that
list by `TestFlavorTable_MatchesTheBuiltinFlavors` in `internal/flavor/flavor_table_test.go`.

| Flavor | Profile | Detected by |
| :-- | :-- | :-- |
| `os-image` | `os-image` | `packer/*.pkr.hcl`, `mkosi.conf`, `build/mkosi.conf` or Kconfig fragments under `kconfig/` |
| `infra-k8s` | `container-image` | `Chart.yaml`, `kustomization.yaml` or `helmfile.yaml` at the root |
| `python-ml` | `app-service` | a machine-learning dependency in `pyproject.toml` or `requirements.txt` |
| `native-gpu-systems` | `native-gpu-systems` | `meson.build` (root, `core/` or `libvmaf/`) or `CMakeLists.txt` |
| `rust-systems` | `native-gpu-systems` | `Cargo.toml` |
| `frontend-svelte` | `app-service` | `package.json` with `svelte.config.js`, `src/routes` or `vite.config.ts` |
| `typescript-node` | `app-service` | `package.json` without `svelte.config.js` or `src/routes` |
| `mobile-flutter` | `app-service` | `pubspec.yaml` |
| `jvm-service` | `app-service` | `pom.xml`, `build.gradle`, `build.gradle.kts`, `mvnw` or `gradlew` |
| `go-service` | `framework` | `go.mod` with `cmd/`, `Dockerfile` or `docker/` |
| `go-library` | `framework` | `go.mod` with `pkg/` or `internal/` |
| `agentic-autonomous` | `framework` | never detected; select it by name |

When your repository matches no row, adoption still succeeds: it scaffolds no flavor templates and
prints a "Not applicable" warning that names `praetorctl flavor apply --flavor=<name>`. `flavor
audit` and `flavor apply` refuse until you either add one of the markers above or pass
`--flavor=<name>`; `gate run` has no such flag, so there the remedy is the marker or a declared
profile that fits.

## Flavor detection does not guess

Detection reports what matched, or reports that nothing did. It no longer falls back to a plausible
answer, because a repository that matched nothing was previously indistinguishable from one that is
a Go library.

- The profile decides which flavors are candidates. A flavor names the stack within a profile, so
  `flavor.Resolve` (`internal/flavor/resolve.go`) first takes the profile `.standards.yaml` declares,
  else the one `internal/classify` reads from the markers, and then picks among the flavors
  implementing it. `flavor audit`, `flavor apply`, `praetorctl adopt` and the Hindsight distiller
  all resolve this way, so they name the same flavor for one checkout. Where the profile has no
  flavors, they report not applicable (`internal/flavor/resolve_test.go`).
- `python-ml` requires a declared machine-learning dependency. It previously fired on any
  `pyproject.toml`, so every Python repository was reported as a PyTorch pipeline and then audited
  against ML tooling it had no reason to install.
- `os-image` is matched by `packer/*.pkr.hcl`, `mkosi.conf`, `build/mkosi.conf` or Kconfig
  fragments under `kconfig/` (a kernel forge), and is tried
  **before** the language flavors. An image forge carries a `go.mod` for its build CLI and a
  `pyproject.toml` for its verification suite, so whichever language flavor claimed it first would
  describe the tooling rather than the product. The Packer marker is a glob and matches only regular
  files: an empty `packer/`, or one holding only a README, is not a forge. See
  [archetype authoring](archetype-authoring.md#the-os-image-flavor-a-forge-is-what-it-builds).
- `agentic-autonomous` never auto-detects and must be selected by name. Its markers — `.agents/` and
  `.paperclip/harness.json` — are both written by adoption itself, so detecting on them describes
  the governance tool rather than the repository.
- Where nothing matches and no flavor is pinned, one decision (`flavor.IsNotApplicable`) governs
  every caller, so adoption and the audit reach the same verdict: `praetorctl adopt` scaffolds no
  flavor templates and warns "Not applicable" (`internal/adopt/flavor_report_test.go`), a bad
  `flavors` pin is an adoption error, never that warning
  (`internal/adopt/flavor_report_test.go`), and
  `flavor audit` prints "Skipped, not applicable" with the same reason and exits 0
  (`cmd/standardsctl/flavor_skip_cli_test.go`). `gate run` records its Flavor Conformance stage as
  not applicable and names the flavors of the profile it tried (`internal/gating/flavor_stage_test.go`).
  The generated pre-push `flavor-audit` job runs `flavor audit .`, so it passes on the same skip. A repository whose
  markers match a flavor but does not conform still fails, a repository no profile classifies (a
  mistyped or empty directory, `flavor.ErrNoProfile`) still fails
  (`cmd/standardsctl/flavor_skip_cli_test.go`), and a `flavors` pin below wins over the skip. `flavor apply` still refuses with `ErrNoFlavorMatched` instead of scaffolding a flavor that
  describes nothing about the repository; pass `--flavor=<name>` to scaffold or audit against one
  deliberately. A flat Go module with only root `.go` files is one such repository.
- `go-service` also implements `app-service` (`flavor.MultiProfile`), tried after the flavors built
  for that profile, so a Go service declaring `app-service` resolves without a flag
  (`internal/flavor/pins_test.go`).
- The `flavors` list in `.standards.yaml` pins the flavor and replaces detection for `flavor audit`,
  `gate run` and the generated pre-push hook, which runs the audit. Each entry names a flavor
  (`praetorctl flavor list`) and optionally a repository-relative `path`; a repository with several
  components lists one entry per path. A path-scoped entry audits the flavor's stack templates and
  toolchains in that directory and the repository-level files at the repository root, where they
  live whichever directory holds the stack; the list is the one below (`internal/flavor/pins_test.go`). An unknown flavor, a missing directory or a path outside the
  repository, symlinks included, is refused, never skipped. An explicit `--flavor` still wins on
  the command line (`flavor.ResolveTargets`, `internal/config/flavor_pins.go`,
  `cmd/standardsctl/flavor_pin_cli_test.go`).

  ```yaml
  flavors:
    - name: go-service
      path: api
    - name: frontend-svelte
      path: web
  ```

  `flavor apply`, `adopt` and the Hindsight distiller read the same pin through the same resolver,
  so they act on the flavor the audit measures. A scaffold writes one flavor at the repository
  root: with a root pin it scaffolds that flavor. A directory-scoped or a second pin gets no
  scaffold: `flavor apply` refuses (`flavor.ErrPinNotScaffoldable`) and `adopt` skips the flavor
  step with that reason and no `--flavor` suggestion. For such pins, keep the repository-level files (`.github/`, `.vscode/`, `.paperclip/`,
  `lefthook.yml`, `.gitleaks.toml`, `.standards.yaml`, `.standards.lock`, `AGENTS.md`, `CLAUDE.md`)
  at the repository root and the stack files (for a Go service `go.mod`, `.golangci.yml`,
  `.gosec.json`, `Dockerfile`) under each pinned path; the list is built from the audit's own
  repository-level lists (`flavor.ScopedPinRemedy`, `internal/flavor/not_applicable_test.go`). To
  scaffold one flavor, pin it once without a path (`internal/flavor/pins_apply_test.go`).
- `flavor apply` also renders the branch ruleset (`.github/rulesets/main.json`) and lists every
  other required setting with the command that writes it
  ([flavor settings](archetype-authoring.md#flavor-settings-the-ruleset-is-rendered-the-rest-are-deferred)).
  It fails when any template or the ruleset could not be written (`flavor.ErrApplyIncomplete`), and
  prints what it created before the failure. `--force` refreshes flavor scaffolds but never rewrites
  `.standards.yaml`, `.standards.lock` or the `.workingdir/` ledger
  (`internal/flavor/scaffold_integrity_test.go`).

## What the flavor score measures

`flavor audit` scores one thing: the share of the flavor's required **templates and settings** that
the repository carries. The bar is 80%, and a missing template fails the audit at any score. The
same commit therefore scores the same on every machine.

| Term | Scored | Checked by |
| :--- | :--- | :--- |
| Templates | yes | the file, or one of its accepted alternatives, exists as a file (a directory of that name does not count) |
| Settings | yes | the file exists **and**, where the setting declares a shape, parses |
| Toolchains | no — advisory | `exec.LookPath` on the machine running the audit |

- **Settings are parsed, not counted.** `lefthook.yml` must parse as a non-empty YAML mapping,
  and for `rust-systems`, which describes it as clippy and rustfmt enforcement, its `pre-commit`
  run lines must call `cargo fmt` and `cargo clippy`
  ([`internal/flavor/lefthook_setting_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/lefthook_setting_test.go));
  `.github/rulesets/main.json` and `.vscode/settings.json` must parse as non-empty JSON objects.
  The ruleset is strict JSON: comments and trailing commas are rejected, the same line
  [`internal/clientsetup`](https://github.com/cordanaLLM/praetor/blob/main/internal/clientsetup/plan.go) draws for client configuration.
  `.vscode/settings.json` is JSON with Comments, the format
  [VS Code documents for it](https://code.visualstudio.com/docs/languages/json): `//` and
  `/* */` comments and a trailing comma are accepted, so a commented settings file counts as valid
  (`DialectOf` in
  [`internal/strictjson/dialect.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/strictjson/dialect.go);
  [`internal/flavor/settings_jsonc_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/settings_jsonc_test.go)).
  In either format a member name repeated in one object is invalid. A
  file that is present but does not parse is reported under **Missing or Invalid Settings** and
  costs its share of the score: it configures no more than a file that is not there. Settings with
  no checkable shape are satisfied by presence, which is all that can be claimed about them.
- **The session ledger is not a template.** `.workingdir/` is git-ignored, so a fresh clone or a
  linked worktree of a conforming repository never carries it; `praetorctl state audit` checks the
  ledger, and `flavor audit` scores only files the repository tracks
  ([`internal/flavor/audit_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/audit_test.go)).
- **Small flavors tolerate no missing setting.** With every template present, a repository passes
  with as many missing or invalid settings as keep it at 80%. That is none for `python-ml`,
  `os-image`, `frontend-svelte`, `jvm-service`, `mobile-flutter`, `agentic-autonomous` and
  `infra-k8s`, and one for every other flavor: `python-ml` carries 1 template and 1 setting, so
  one absent `.vscode/settings.json` scores 50%. The three ledger files used to count as present
  templates on checkouts that carried them, which bought each of those flavors one more gap
  (`go-service` two). `TestAuditFlavor_Boundary_SettingsGapTolerancePerFlavor` in
  [`internal/flavor/audit_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/audit_test.go)
  pins the number for every flavor.
- **Toolchains never decide pass or fail.** They are resolved from `PATH`, so scoring them measured
  the auditing machine: a conforming `go-service` repository scored 11/15 = 73.3% and failed the
  bar on a host with none of its four tools installed, inside the pre-push hook adoption generates.
  `flavor audit` still lists what is missing, with an install command for each, under **Missing
  Toolchains (advisory)**.

Fixtures pinning the exact scores live in
[`internal/flavor/audit_score_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/audit_score_test.go) and
[`internal/flavor/settings_audit_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavor/settings_audit_test.go).
