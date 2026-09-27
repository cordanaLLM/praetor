# Repository Onboarding Guide: cordanaLLM/praetor

Onboard an existing repository into the cordanaLLM declarative governance fleet.

```figure
onboarding-path
```

## 1. Quickstart Onboarding Command

The five-step onboarding pipeline requires a repository with canonical `AGENTS.md` instructions: `praetorctl init` compiles the agent files from it and writes none without it.

[`praetorctl adopt`](../adoption.md) is an alternate entry point, not a step before `init`. One adopt run writes what steps 1 to 4 write (manifest, lockfile, debt baseline, `AGENTS.md` when it is missing, vendor files and DevContainer; `adoptSteps` in `internal/adopt/adopt.go`), so after it you continue at step 5, `praetorctl audit`. Do not run `praetorctl init` afterwards: it refuses because `.standards.yaml` already exists (`ensureManifestAbsent` in `cmd/standardsctl/init.go`).

Execute the onboarding pipeline in your repository root:

```bash
# 1. Initialize configuration with declared profiles and compile the agent files from AGENTS.md
praetorctl init --profile framework --facets security:high,api:public-contract

# 2. Recompile the agent files and add persona copies (rerun after every AGENTS.md edit)
praetorctl compile-context

# 3. Snapshot legacy technical debt infractions to prevent CI failure
praetorctl baseline --record --allow-increase --reason "<why>"

# 4. Prepare a portable devcontainer from reviewed Praetor sources
praetorctl devcontainer generate --source-root /path/to/reviewed/praetor

# 5. Verify the configured governance contract
praetorctl audit
```

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
| **1. Scaffolding** | Create declarative configuration and compile agent files | `praetorctl init` | `.standards.yaml`, `.standards.lock` and a zero-debt `.standards-baseline.json` created; the text register spliced into `AGENTS.md`; the six vendor files `CLAUDE.md`, `.cursor/rules/hiss-invariants.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md` and `.codex/rules.md` compiled (`initAgentContext` in `cmd/standardsctl/init.go`). |
| **2. Context Recompilation** | Recompile vendor files and add persona copies | `praetorctl compile-context` | The vendor files rewritten from `AGENTS.md` (all six unless `agent_clients` in `.standards.yaml` selects fewer); each persona in `.agents/agents` copied to `.claude/agents`, `.github/agents`, `.gemini/agents` and `.codex/agents`. `praetorctl compile-context --verify` checks the same files and writes nothing. |
| **3. Brownfield Baselining** | Snapshot legacy debt | `praetorctl baseline --record --allow-increase --reason "<why>"` | `.standards-baseline.json` populated with existing debt. |
| **4. Devcontainer Setup** | Prepare a portable bootstrap | `praetorctl devcontainer generate --source-root /path/to/reviewed/praetor` | JSON and exact source companions prepared; build and startup remain separate checks. |
| **5. Audit Verification** | Verify configured governance and debt-ratchet gates | `praetorctl audit` | Every executed gate reports pass; skipped or unsupported coverage remains explicit. |

---

## 3. Brownfield Technical Debt Ratcheting

Legacy infractions recorded in `.standards-baseline.json` will not fail CI status checks:

- **Monotonic Ratchet**: Technical debt must decrease over time ($V_{\text{total}}(t_1) \le V_{\text{total}}(t_0)$).
- **Touched-File Clean Rule**: Any legacy file modified during a pull request revokes previous exemptions and must be refactored clean.
- **Initial Debt & Waivers**: Because `praetorctl init` seeds a clean baseline (0 infractions), recording existing debt in a brownfield repository represents an initial increase and requires an explicit, justified increase: `praetorctl baseline --record --allow-increase --reason "<why>"`. The same HISS-13 exception path applies to any subsequent unavoidable architectural exception that raises the recorded count. The reason is stored in the baseline alongside the raised count; there is no CLI command or file that mints a standalone signed waiver. The `hiss-waiver` repository label is a scaffolded taxonomy entry with no enforcement attached.

---

## Flavor detection does not guess

Detection reports what matched, or reports that nothing did. It no longer falls back to a plausible
answer, because a repository that matched nothing was previously indistinguishable from one that is
a Go library.

- A declared profile **narrows** detection: candidates are restricted to the flavors implementing
  that profile, and detection picks among those. Where the profile has no flavors, `flavor audit`
  reports not applicable.
- `python-ml` requires a declared machine-learning dependency. It previously fired on any
  `pyproject.toml`, so every Python repository was reported as a PyTorch pipeline and then audited
  against ML tooling it had no reason to install.
- `os-image` is matched by `packer/*.pkr.hcl`, `mkosi.conf` or `build/mkosi.conf`, and is tried
  **before** the language flavors. An image forge carries a `go.mod` for its build CLI and a
  `pyproject.toml` for its verification suite, so whichever language flavor claimed it first would
  describe the tooling rather than the product. The Packer marker is a glob and matches only regular
  files: an empty `packer/`, or one holding only a README, is not a forge. See
  [archetype authoring](archetype-authoring.md#the-os-image-flavor-a-forge-is-what-it-builds).
- `agentic-autonomous` never auto-detects and must be selected by name. Its markers — `.agents/` and
  `.paperclip/harness.json` — are both written by adoption itself, so detecting on them describes
  the governance tool rather than the repository.
- Where nothing matches, `flavor audit` and `flavor apply` refuse with `ErrNoFlavorMatched` instead
  of scoring the repository against a flavor that describes nothing about it. Pass `--flavor=<name>`
  to audit against one deliberately. `praetorctl adopt` scaffolds no flavor templates there and
  prints a "Not applicable" warning naming `praetorctl flavor apply --flavor=<name>` instead
  (`internal/adopt/flavor_report_test.go`); a flat Go module with only root `.go` files is one such
  repository.
- `flavor apply` fails when any template could not be written (`flavor.ErrApplyIncomplete`), and
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

- **Settings are parsed, not counted.** `lefthook.yml` must parse as a non-empty YAML mapping;
  `.github/rulesets/main.json` and `.vscode/settings.json` must parse as non-empty JSON objects
  (strict JSON — comments and trailing commas are rejected, the same line
  [`internal/clientsetup`](https://github.com/cordanaLLM/praetor/blob/main/internal/clientsetup/plan.go) draws for client configuration). A
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
