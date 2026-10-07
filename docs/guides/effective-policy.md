# Effective audit policy

CLI `praetorctl audit`, MCP `standards_audit` and retained public adoption loops
resolve complexity from the same `internal/config` implementation. The existing
HISS scanner consumes the resolved `max_func_loc`. CLI/MCP audits report the same
effective digest for the same input snapshots and show the sources that impose
the limit. Public-loop reports retain the planned policy and each verification's
policy digest, applied limit and baseline evidence.

The first migration covers audit function length. Cyclomatic complexity, cognitive
complexity and statement limits are resolved and retained in the typed policy; the
HISS-04 scan measures against them and fails on none. `.config/archetype-coverage.yaml`
names every function that reads each key, including the generated editor settings that
receive the cyclomatic and statement limits.
Routing, budgets, credentials and deployment activation are separate consumers.

Pinned profiles and facets contribute every lattice dimension, not only
complexity: branch protection, supply chain, memory, error unwraps, linters and
DevContainer features. [The strictness lattice](archetype-authoring.md#3-the-strictness-lattice-highest-standard-wins)
lists each dimension's join rule. `praetorctl adopt` renders
`.github/rulesets/main.json` from the resolved branch protection, and
`praetorctl sync` and the audit's locked documentation gate check the ruleset
against that same policy (`TestSyncRulesetFollowsTheJoinedProfileBranchProtection`,
`TestAuditDocumentationGateUsesTheEffectiveBranchProtection`). When the policy
does not resolve, for example because the pinned catalog is not materialized and
no `--catalog-root` is given, sync has no policy to check against: it prints
`[UNVERIFIED]`, neither validates nor synthesizes the ruleset, and exits
incomplete without reaching the forge. A lock the catalog cannot verify is the
same cause and is counted once
(`TestSyncLeavesTheRulesetUncheckedWhileThePolicyIsUnresolved`,
`TestSyncCountsAnUnresolvedPolicyOnceBesideAVerifiedLock`). Linters, memory
and error unwraps are resolved and sealed into the digest; no gate executes the
linter list yet, and the coverage manifest records all three as `unconsumed`
([Every key names its consumer](archetype-authoring.md#every-key-names-its-consumer)).

Public adoption loops resolve their policy during the dry run, then independently
scan the original source under that policy before applying changes. Applied
policy must match the plan. Verification compares the saved baseline's entries
with the independent original scan and evaluates current violations against that
anchor. Unchanged legacy debt is retained; new violations and violations in
touched files fail. Old public-loop reports produced with a fixed scanner limit
do not establish verification under a stricter policy; rerun those cases.
Public loops currently select repository and pinned-catalog constraints;
external fleet/organization/deployment/workstation paths are audit arguments,
not public-loop options.

## Sources and strictness

Resolution uses these sources in a fixed order:

1. Built-in defaults (`config.DefaultPolicy`): cyclomatic 15, cognitive 20,
   `max_func_loc: 60`, statements 75.
2. The repository's `.standards.lock` and its pinned, materialized profiles/facets.
3. Explicit fleet, organization, deployment and workstation files, in that order.
4. Repository `overrides.complexity` from `.standards.yaml`.
5. The audit compatibility constraint: `max_func_loc: 60`.

Repository `overrides.branch_protection` and `overrides.supply_chain` apply after
the join. They only tighten, except `review_mode: single_maintainer`, the one
explicit relaxation; a profile or facet cannot set `review_mode`.

`overrides.actions` and `workflow_runs` take no part in the join: the audit reads them from the
repository manifest and compares them with the live forge ([Live Actions checks](actions-live-checks.md)).
The loader still holds both to the manifest rules, so a malformed declaration fails the policy
resolution instead of reaching the forge (`TestActionsPolicy_Negative_EffectiveLoaderValidates`).

Every complexity constraint can only tighten an earlier limit. A later limit of
`90` cannot override an existing `50`. Omitted fields do not contribute. In
external files and repository overrides, explicit zero, negative, null,
noninteger and unknown complexity fields are errors. A profile or facet may
state `0` for "no bound", as `upstream-fork` does; that limit contributes
nothing, like an omitted one (`TestLoadEffectivePolicyLooserFacetNeverLoosens`).

Both `60`s are one constant. `hiss.DefaultMaxFuncLOC` in `internal/hiss/hiss.go`
is the scanner's default; the built-in default, the compatibility constraint
(`config.AuditMaxFuncLOC`), adoption's legacy-debt scan and the editor, language
server and MCP inspection fallbacks all read it. Because the built-in default
already is the audit's length, the compatibility constraint never lowers the
limit; in an audit it only joins `builtin:defaults-v1` as a tied contributor.
`internal/config/func_loc_default_test.go` pins every consumer to the constant.

### What `plan` reports

`praetorctl plan` resolves this same policy, with the audit compatibility constraint applied, so a
dry run previews what the audit will enforce, pinned profiles included: `plan` and `audit` report the
same limits. Like `sync` and `audit`, `plan --catalog-root` (MCP `standards_plan`: `catalog_root`) reads the
pinned profiles from a catalog outside the repository; without it a lock whose catalog is not
materialized fails the preview (`TestPlanEffectivePolicy_Boundary_CatalogRootSelectsThePinnedCatalog`).
After the policy, `plan` previews the live Actions workflow permission comparison when it can read
the forge, and never fails on it; `plan --offline` skips it ([Live Actions checks](actions-live-checks.md)).

Resolving the policy needs a lockfile. In a repository that has not been adopted there are no pinned
profiles, so defaults plus the repository's overrides is the whole policy, and `plan` says so:

```text
[INFO] no .standards.lock: built-in defaults and repository overrides only
```

That preview states a function length of 60 there, the length the first audit after adoption
enforces, and so does the MCP `standards_plan` tool, which reports built-in defaults plus overrides
(`cmd/standards-mcp/server.go`). The built-in default used to be 100, so a lock-less preview
promised a length no audit accepted, and an override above 60 showed up in it.

### Consequence of the 60-line default

Because strictness only tightens, `max_func_loc: 60` wins against any archetype declaring more,
with or without the audit compatibility constraint. Ten of the fourteen shipped archetypes declare
a looser bound -- `template-seed` 100, `app-service` 80, seven at 75, `library-client` 70 -- and
every one of them resolves to 60.

An archetype's declared `max_func_loc` above 60 is therefore documentation of intent rather than an
effective limit. Read the `audit` output, not the archetype file, to learn what a repository is held
to. A repository can still tighten the limit with `overrides.complexity.max_func_loc`; nothing can
loosen it. The HISS-04 row adoption writes into `AGENTS.md` and the Paperclip harness states the
resolved length, and adds `(audit ceiling)` when that length is the 60-line ceiling. It states the
resolved `max_cyclomatic`, `max_cognitive` and `max_statements` too, which the audit does not cap,
so an override of any of the four reaches the row
([what the generated harness claims](adoption-verification.md#what-the-generated-harness-claims)).

An explicitly selected external file must contain at least one owned root section:
`complexity`, `backlog` ([Backlog caps](#backlog-caps)), or one of the operator sections
`clients`, `hooks`, `update`, `framework`, `forge` and `topology` described in
[Operator settings](#operator-settings).

```yaml
complexity:
  max_func_loc: 50
```

An empty mapping is valid and contributes nothing. Other existing sections may
coexist, but this resolver does not activate them. A misspelled root key or an
unsupported-only document fails instead of pretending a policy was applied.

## Operator settings

The same external documents carry the operator's settings for agent clients, the
agent-hook command policy, workstation updates, and the operator data described in
[Framework, forge and topology](#framework-forge-and-topology). There is no second settings
file: a fleet, organization, deployment or workstation document may hold these
sections beside `complexity`, or on their own. They go through the same loader and
bounds, and into the same effective digest.

`audit` resolves and seals these sections but does not act on them. Host commands
select the fleet and workstation documents through `config.SelectOperatorSettings`
and load the same schema through `config.LoadOperatorSettings` (or
`config.SelectOperatorPolicy`, which keeps the policy for relative paths). `praetorctl
hook` consumes `hooks`; `praetorctl clients permissions` consumes the AGY permission
selection under `clients`; the `needs` commands consume `framework` and
`forge.default_owner`. `issue`, `milestone`, `project`, `init` and `forge
sync-wiki` consume `forge.default_owner` too, and `issue reconcile` also reads
`forge.reconcile_repos`; `topology audit` and `topology clean` consume
`topology.org_containers`. Repository policy and host activation therefore share
one schema without making `audit` inspect a user's home directory.

A workstation document, the layer that holds host paths:

```yaml
clients:
  selected:
    agy:
      binary: /home/operator/.local/bin/agy
      permissions:
        manage: true
        allow: ["mcp(hindsight/hindsight_list_knowledge_pages)"]
hooks:
  command_policy:
    deny: ['\bexample-org/']
update:
  checkout: /home/operator/dev/example/praetor
```

### Keys and defaults

Keys inside every operator section are strict. A misspelled key, an
unknown client or a value of the wrong type fails the document and names the key.
Every string passes the literal rule shared with the MCP registry
(`internal/util/literal.go`): no control byte, `$`, backtick, `{env:` or `{file:`.

| Key | Default | Accepted values |
| :-- | :-- | :-- |
| `clients.mode` | `advisory` | `advisory` reports an ungoverned client and lets work continue; `strict` makes the client commands fail instead: a launch below `discovered`, a verify of a required client below `observed` |
| `clients.verified_max_age` | `168h` | a duration from `1h` to `2160h` |
| `clients.govern` | `present` | `present` governs every known client installed on the host; `listed` governs only `clients.selected` |
| `clients.selected.<id>` | every known client | `<id>` is one of `agy`, `claude`, `cline`, `codex`, `continue`, `gemini`, `kilo`, `opencode-v1` (`internal/clientid`) |
| `clients.selected.<id>.required` | `true` | boolean |
| `clients.selected.<id>.scopes` | `[global]` | `global` and/or `workspace`, each at most once |
| `clients.selected.<id>.plugin` | `true` | boolean |
| `clients.selected.<id>.binary`, `.config_root` | empty (look up at run time) | clean absolute path; workstation layer only |
| `clients.selected.<id>.registry`, `.connection_profile` | empty | clean path, relative to the file that sets it, or absolute |
| `clients.selected.<id>.permissions.manage` | `false` | boolean; `true` appends the listed grants to the client's own list and never removes or widens one |
| `clients.selected.<id>.permissions.allow` | `[]` | at most 32 literals of up to 512 bytes, merged across layers |
| `hooks.scope` | `governed` | `governed` guards repositories that carry `.standards.yaml`; `all` guards every workspace |
| `hooks.command_policy.deny` | `[]` | at most 64 RE2 patterns of up to 512 bytes, compiled at load |
| `hooks.python` | `[python3, python, [py, -3]]` | up to 8 candidates: a bare command, or a list of a command and up to 8 arguments; an absolute path only in the workstation layer |
| `update.channel` | `push` | `push`; `release` is reserved until release artifacts ship |
| `update.source` | `checkout` | `checkout`; `artifact` is reserved |
| `update.checkout`, `update.bin_dir` | empty | clean absolute path; workstation layer only |
| `update.remote` | `origin` | 1 to 64 letters, digits, `.`, `_` or `-`, starting with a letter or digit |
| `update.branch` | `main` | 1 to 128 of the same plus `/`, without `..` |
| `update.pin` | empty | a 40-character lowercase commit id |
| `update.interval` | `15m` | a duration from `5m` to `24h` |
| `update.require_signed` | `true` | boolean |
| `update.allowed_signers` | empty | clean path, relative to the file that sets it, or absolute |
| `update.receipt_public_key` | empty | 64 lowercase hex characters (Ed25519) |

The engine ships no deny pattern and manages no client's grant list. Both are
operator decisions: organisation names belong in `hooks.command_policy.deny`, and
an operator host opts in to grant management by setting `permissions.manage: true`
for a client in its own layer, as the example above does for `agy`. Adopters who
set neither get neither.
`config.ValidateCommandPolicyDeny` is the one check for those bounds; the hook
policy validates through it.

AGY 1.2.7 grants use exact `action(target)` rules. The managed adapter accepts
`read_file`, `write_file`, `read_url`, `execute_url`, `command` and `mcp`; an MCP
target names exact `server/tool`, `server/*`, or global `*`. File and URL rules
accept only the documented global `*`, not partial globs. A `read_url` or
`execute_url` target is a bare lowercase host or IP literal, such as
`read_url(example.test)`: AGY matches URL rules by hostname and subdomain, so a
scheme, port, path or userinfo is rejected, as is a DNS name whose last label is
decimal or `0x`-hexadecimal, which WHATWG URL parsing reads as IPv4
(`httpendpoint.CanonicalHost`, tested in `internal/httpendpoint/https_test.go` and
`internal/clientsetup/agy_permissions_test.go`). Existing ask/deny URL rules are
compared by their host through `net/url`, but only when that host is unambiguous:
userinfo, a scheme without `//` (`https:example.test`), a DNS name with a bare
port (`example.test:443` is lexically `scheme:opaque`), an empty or out-of-range
port, a percent sign, a backslash, or a host that is not canonical makes planning
fail closed with a `cannot prove non-overlap` diagnostic. The operator loader keeps the grant strings generic so
future clients can carry their own syntax, while `clients permissions` validates
the selected AGY rules before producing or changing a settings file.

An allow rule controls which matching call can proceed without approval. Do not
infer an active-workspace exception: an AGY 1.2.7 headless replay denied a native
read inside its active workspace when no matching `read_file(...)` grant was
loaded. A governed read-only review therefore starts AGY in a separate disposable
workspace and explicitly grants the reviewed repository through
`read_file(...)`. Starting AGY inside the reviewed repository does not make that
repository readable or read-only by policy.

### Framework, forge and topology

These sections carry operator data that earlier releases shipped as built-in values
([ADR-0014](../adr/0014-operator-neutral-defaults.md)). Every key is optional and empty
by default, and an empty value never falls back to a maintainer's value. A document
without these sections keeps the digest it had before they existed
(`internal/config/operator_framework_test.go`).

| Key | Default | Accepted values | Read by |
| :-- | :-- | :-- | :-- |
| `framework.targets.<lang>` | absent | `<lang>` is `go`, `typescript`, `python`, `rust` or `native` (`config.FrameworkLanguages`) | every `needs` subcommand, the MCP `standards_needs_report` tool and the needs-miner agent |
| `framework.targets.<lang>.module` | empty | a module path whose first element is a host, at most 256 bytes (`config.IsModulePathShaped`) | the framework the language's demands are scored against |
| `framework.targets.<lang>.builder_kits` | `[]` | at most 8 `<owner>/<name>` coordinates, each once | the scan's builder kits; entry 0 routes `needs requests`, and its owner is the request's `target_org` |
| `framework.targets.<lang>.contract` | empty | clean path, relative to the file that sets it, or absolute | a version-1 capability contract declaring the framework's packages |
| `framework.targets.go.checkout` | empty | clean absolute path; workstation layer only; accepted for `go` only | the framework checkout the `needs` commands observe |
| `framework.migration_branch` | empty, meaning `refactor/framework-adoption` | a branch name as for `update.branch` | the branch `needs migrate` reports for an admitted migration |
| `forge.default_owner` | empty | a GitHub owner: 1 to 39 letters, digits and single hyphens | the last owner step of `config.ResolveRepositoryIdentity`, used by `needs epic --publish`, `issue`, `milestone`, `project`, `init` and `forge sync-wiki` |
| `forge.reconcile_repos` | `[]` | at most 256 `<owner>/<name>` coordinates | the `issue reconcile` scope when `--repos` is not given; without either, the current repository |
| `forge.review_bot` | empty | a GitHub owner, optionally followed by `[bot]` | validated and sealed; `forge.AssignReviewers` takes the bot list from its caller, and no command passes this key yet |
| `topology.org_containers` | `[]` | at most 64 names of lowercase letters, digits, `.`, `_` and `-`, merged across layers | `praetorctl topology audit` and `topology clean`, which add the names to the built-in containers (`topology.OrgContainers`) |

A workstation document configuring one framework, with placeholder values:

```yaml
framework:
  targets:
    go:
      module: example.com/acme/kit
      builder_kits: [acme/kit]
      contract: frameworks/kit.capabilities.yaml
      checkout: /home/operator/src/kit
forge:
  default_owner: acme
```

How the `needs` engine reads the targets (`internal/needs/targets.go`):

- **No target configured.** Praetor ships no framework. Every `needs` command still
  classifies dependencies into capabilities and reports the framework as not configured:
  mapping availability renders as `n/a (no target framework configured)`, never 0%,
  `needs migrate --apply` and `needs epic --publish` refuse, and requests are unrouted
  (ADR-0014 §4; the full table is under
  [Not configured](needs-capability-evidence.md#not-configured)).
- **Any target configured.** Only the configured languages have a framework. A demand
  is mapped only when the target's contract declares a package for it; otherwise it
  keeps its capability and is a gap.
- **Framework selection** for `report`, `aggregate`, `migrate`, `epic` and
  `contract export`: `--framework`, then `$PRAETOR_FRAMEWORK_DIR`, then
  `framework.targets.go.checkout`; without a checkout, `framework.targets.go.contract`
  declares the framework (coverage basis `catalog-declared`); without a contract the go
  target's module names it. Nothing selected reports
  `Framework: not configured (set framework.targets.<lang>.module and .contract, or pass --framework)`.
  The former default checkout under the dev root is gone.
- **Contracts.** A contract's `ecosystem` must match its language (`go`, `npm`, `pypi`,
  `cargo`, `system`). `praetorctl needs contract export --language=<lang> --out=<file>`
  writes the framework a target resolves to as a contract that can be configured in its
  place; see [needs capability evidence](needs-capability-evidence.md).

Every `needs` subcommand accepts `--fleet-config`, `--workstation-config` and
`--manifest`, the flags `clients permissions` uses; the selection order is the one
below.

### How layers merge

Layers apply in the order listed under [Sources and strictness](#sources-and-strictness):
fleet, organization, deployment, workstation.

- A later layer replaces a scalar. `EffectivePolicy.OperatorFields` records the
  layers that set each concrete key, for example
  `clients.selected.agy.permissions.allow`.
- Once a layer sets `clients.mode: strict`, `clients.govern: present`,
  `required: true`, `update.require_signed: true` or `hooks.scope: all`, a later
  layer cannot loosen it. The error names both layers. A built-in default is not a
  layer, so the first layer may choose either value.
- `hooks.command_policy.deny` and `permissions.allow` append without duplicates.
  The bound applies to the merged list, so 40 fleet patterns plus 25 new
  workstation patterns fail.
- `topology.org_containers` appends the same way, bounded after merging;
  `framework.targets.<lang>.builder_kits` and `forge.reconcile_repos` are replaced
  by a later layer.
- Two layers giving one client different non-empty `registry` values fail.
- Client binaries, config roots, the fork checkout, `bin_dir`, the framework checkout
  and absolute interpreter paths are host data. Any layer other than `workstation`
  that sets one fails.

A relative `registry`, `connection_profile`, `allowed_signers` or framework `contract`
value keeps its spelling in the digest, so moving the documents does not change it.
`EffectivePolicy.ResolveOperatorPath` resolves it against the directory of the
file that set it.

A policy whose layers carry no operator section keeps the digest it had before
these sections existed. The operator sections therefore did not invalidate
retained plans and repair anchors; the 60-line built-in default did (see
[Provenance and migration](#provenance-and-migration)).

### Which documents the hook, client and workstation commands use

`audit` and `gate` take the explicit flags above and read nothing else. For the
hook, client, workstation and `needs` commands and the MCP `standards_needs_report`
tool, `config.SelectOperatorSettings` in `internal/config/install_manifest.go`
resolves each document in this order (hook, the needs-miner agent and the MCP tool
take no flags, so they start at step 2):

1. The command's `--fleet-config` or `--workstation-config` flag.
2. `PRAETOR_FLEET_CONFIG` or `PRAETOR_WORKSTATION_CONFIG`.
3. The path the install manifest recorded. The manifest is
   `praetor/install.json` under the per-user configuration directory; the
   `workstation install` and `workstation update` commands write it when they
   ship. The recorded path is used only
   while the file's bytes still match the digest the manifest recorded. A changed
   or missing file is an error that says to rerun `workstation update` or pass the
   path explicitly.
4. Not configured: the built-in defaults.

There is no separate pointer file. The manifest reader rejects unknown and
duplicate members and checks every field.

Tests: `internal/config/operator_sections_test.go` (four-layer merge with
contributors, loosening, host data, bounds, digest),
`internal/config/operator_framework_test.go` (framework, forge and topology keys,
the digest golden and the replay of `testdata/operator/framework.yaml`),
`internal/config/repository_identity_test.go` (owner resolution) and
`internal/config/install_manifest_test.go` (manifest and selection order), with
fixtures under `internal/config/testdata/operator/`.

## Backlog caps

A backlog cap bounds how many open items one backlog category may hold (#792). Declare it in
`.standards.yaml`, in a profile or facet, or in an external document, all under the same keys:

```yaml
backlog:
  caps:
    defects:
      max: 80
      action: gate
    questions:
      max: 10
```

`praetorctl state status` then prints each capped category after the session lines:

```text
  Backlog Cap:       defects: 81 of 80, OVER the cap (max 80 set by repository, action gate set by repository)
  Backlog Cap:       questions: 4 of 10, under the cap (max 10 set by repository, action report set by builtin default)
                     defects: run 'praetorctl state batch' to write its batch
```

A category no layer caps has no bound, and `state status` prints nothing for it.

### Categories

| Category | What is counted | Re-check before a batch |
| :-- | :-- | :-- |
| `defects` | rows of `.workingdir/BUGS.md` whose status is `open` or `investigating` and whose kind is not `scope` ([row kinds](state-ledger-integrity.md#row-kinds)); a row without a kind is counted and reported as a finding | the bug ledger's location re-check (`bugledger.CheckLocation`, the check `praetorctl bugs audit` runs): a Go `file:line` that no longer resolves is marked; any other location is marked not re-checked |
| `tasks` | every open item of the state ledger: the pending `- [ ]` rows of `.workingdir/OPEN.md` (the rows `praetorctl state task list` numbers, named `task <n>`), then the pending rows of `.workingdir/BACKLOG.md`, the deferred workstreams (named `BACKLOG.md:<line>`). The discharged sections hold the completed rows `state task archive` moves there, so they count nothing; a pending row under a `Discharged` heading is counted and reported as a finding | none; each item is marked not re-checked |
| `questions` | pending rows of `.workingdir/QUESTIONS.md` | none; each item is marked not re-checked |
| `forge_alerts` | nothing yet: praetor has no reader for code-scanning, dependency or secret-scanning alerts, so the category prints as `not counted` with that reason, never as 0 | none |

The counts come from the ledger readers in `internal/state` (`internal/backlogcap/backlogcap.go`);
`BACKLOG.md` is read by `state.ListBacklogTasksContext`, the `OPEN.md` task parser applied to it
(`TestBacklogCap_Positive_TasksCountBacklogWorkstreams`,
`TestBacklogCap_Negative_BacklogRowsAloneTripTheGate`). An absent ledger holds no items, and the
line says which file is absent.

### Keys, boundary and actions

| Key | Accepted values |
| :-- | :-- |
| `backlog.caps.<category>.max` | an integer from 1 to 1,000,000 |
| `backlog.caps.<category>.action` | `report` (the default once a max is declared), `batch` or `gate` |

The cap is inclusive, like `max_func_loc`: `max` is the largest count within the cap. A count
equal to `max` is at the cap and passes; only a count above it is over the cap and triggers the
action (`TestBacklogCap_Boundary_ExactlyAtTheCapPasses`,
`TestBacklogCap_Negative_OneOverTheCapBatchesEveryItemAndGateFails` in
`internal/backlogcap/backlogcap_test.go`).

Each action also does what the weaker ones do:

- **`report`**: `state status` marks the category over its cap.
- **`batch`**: `praetorctl state batch [dir] [--date=YYYY-MM-DD]` writes
  `.workingdir/batches/<category>-<date>.md`. It lists every counted item once, grouped by file
  for `defects`, by ledger and the heading above the row for `tasks` (`OPEN.md: In-Flight Tasks`,
  `BACKLOG.md: Future Workstreams`), and as one group for `questions`; each
  item carries its re-check verdict, and the findings follow. The date defaults to today in UTC,
  and the same input writes the same bytes (`TestWriteBatches_DateAndDeterminism`).
- **`gate`**: `praetorctl audit` fails, naming the category, its count and its cap:
  `[FAIL] backlog cap defects: 81 items, over the cap of 80 (action gate, max set by repository)`.
  A gate on a category praetor cannot count fails too, because a gate that cannot count is not a
  passing gate (`TestAudit_Negative_BacklogCapGateFailsTheAudit` in
  `cmd/standardsctl/state_backlog_test.go`).

The audit counts the ledgers of the checkout it audits. A checkout without `.workingdir`, such
as a CI clone, holds no ledger, so each category counts 0 and its `[INFO] Backlog cap` line says
the ledger is absent. The MCP `standards_audit` tool does not run this gate and names it among
the CLI-only gates in its summary.

An unknown category, an unknown key and an unknown action fail the document and name the key,
for example `backlog.caps: unknown category "bugs"`. An action that no layer gives a `max` to act
on fails the resolution (`TestBacklogCaps_Negative_ManifestValidationNamesTheKey`,
`TestBacklogCaps_Negative_ActionWithoutMaxFails` in `internal/config/backlog_caps_test.go`).

### Where a cap comes from

Caps resolve through the [sources](#sources-and-strictness) complexity resolves through, and
join the same way: a cap only tightens. The lowest declared `max` and the strictest action win
whichever layer declares them, so a fleet default, a profile override and a repository override
each win when each is tighter than the one before, and nothing loosens a cap a layer imposed
(`TestBacklogCaps_Positive_FleetProfileRepositoryEachWinInOrder`,
`TestBacklogCaps_Negative_LooserLayerNeverLoosens`). `max` and `action` join separately: a fleet
`gate` with a repository `max` yields the repository's `max` gated.

`EffectivePolicy.BacklogFields` names the layers that set each value, keyed
`backlog.caps.<category>.max` and `.action`; an equal value from two layers names both. The audit
evidence line prints it, `backlog.caps.defects: max=30 (repository) action=gate (repository)`, and
`state status` prints it as `set by`. An undeclared action is `report`, printed as set by the
`builtin default`. A policy that declares no cap leaves both out of its digest, so its effective
digest is unchanged.

`state status` and `state batch` take the audit's source flags (`--catalog-root`,
`--fleet-config`, `--organization-config`, `--deployment-config`, `--workstation-config`) and
discover nothing implicitly. A repository without `.standards.yaml` has no caps; one without
`.standards.lock` resolves without pinned profiles and facets, as `plan` does
(`config.LoadUnadoptedEffectivePolicyContext`). A policy that does not resolve is printed by
`state status` as `Backlog Cap: unresolved: <error>`, never omitted.

## Selecting a shared or private configuration

```sh
praetorctl audit --config /workspace/project/.standards.yaml \
  --catalog-root /opt/praetor-catalog \
  --fleet-config /configuration/fleet.yaml \
  --organization-config /configuration/organization.yaml \
  --deployment-config /configuration/container.yaml \
  --workstation-config /configuration/workstation.yaml
```

Each path is optional. Omission means no external layer: files in a home directory
or repository are not automatically discovered. An explicitly selected missing
file fails. The catalog root contains `.config/archetypes` and its `facets`
subdirectory; every declared profile/facet must match the repository lock digest.
The default catalog root is the audited repository. A file that no longer matches
fails with `ErrLockDigestMismatch`, naming the profile or facet, the pinned digest,
the file and the digest it hashes to, in the same words the lockfile gate uses
(`entryDigestMismatch` in `internal/config/lockdigest.go`, tests in
`internal/config/lock_mismatch_report_test.go`), so the lock can be re-pinned from
the message.

Adoption writes the pinned catalog into the repository's own `.config/archetypes`
and, like every other file it writes, asks git whether the repository's ignore
rules exclude it. An excluded file is still written, so a local audit works, but
adoption reports an error for it: git will not commit the file, and a clean
checkout or CI run then audits without its pinned catalog. A kernel-style tree
that ignores a bare `.config` is the usual cause; there the `git-ignore` step
re-includes the root directory with `!/.config/` in the managed block, and a declined
step gets the same rule proposed in the error. When git cannot answer (not
installed, not a work tree) adoption states the skipped check as a warning
([files the repository ignores](../adoption.md#what-adoption-reads-before-it-writes)).
Tests: `internal/adopt/ignored_paths_test.go`, `internal/adopt/ignored_writes_test.go`.

The same explicit configuration can be mounted into a container, bot, plugin or
workstation. A private GitOps fork can own these files while consuming the public
application. These flags configure one audit invocation; they do not install a
global service or activate policy in other tools.

The corresponding optional MCP arguments are `catalog_root`, `fleet_config_path`,
`organization_config_path`, `deployment_config_path` and `workstation_config_path`.
They retain the server's existing path-confinement policy. Supplying a path does
not grant permission to read outside the server root. Operator-global server
configuration remains a separate integration step.

## Provenance and migration

Resolution reads bounded regular-file snapshots, rejects symlinks, and hashes the
text decoded. A source in one consistent line-ending style hashes as its LF form,
so a Windows checkout (CRLF under `* text=auto`) reports the source hashes and the
effective digest of an LF clone, and its archetypes match the lock pins; a source
with mixed line endings hashes byte for byte, and a pin mismatch says so
(`util.CheckoutTextDigest` in `internal/util/line_endings.go`, tests in
`internal/config/checkout_line_endings_test.go` and
`cmd/standardsctl/checkout_line_endings_test.go`). Unsupported YAML aliases,
duplicate keys and multiple documents fail. Pinned sources must exist locally in the selected catalog;
lock metadata alone cannot establish the policy that was applied.

The effective digest includes ordered source identities and hashes, effective
policy values and field contributors. Diagnostic filesystem paths are excluded,
so relocating an identical catalog or mounting the same configuration elsewhere
does not change its identity. CLI/MCP text includes at most 16 source hashes and
an explicit omitted count. The typed result retains every source and contributor.
Memory and error unwraps are omitted from the encoding while unset, so a plan or
repair anchor retained before those dimensions existed still verifies
(`TestResolvedPolicyOmitsUnsetDimensionsFromItsEncoding`); a freshly resolved
policy carries both, so its digest differs from the one an older release reported.

The built-in function length is 60 lines (BUG-309), the same cap the audit
enforces. The `builtin:defaults-v1` source hash encodes the default values, so a
changed default changes every effective digest. Unless a layer tightens function
length below 60, an audit lists `builtin:defaults-v1` beside
`builtin:audit-compat-v1` as a `max_func_loc` contributor. Public-loop plans and
repair anchors recorded under a different default fail with `public verification
policy differs from the planned policy` (`internal/dogfood/repair_validate.go`);
rerun the dry run to record a new anchor.

Existing repositories with lock pins but no local archetypes must either select a
matching source bundle with `--catalog-root` or materialize the pinned catalog
during adoption. The audit's lockfile digest gate hashes against that same catalog
and fails when none is materialized; the outcomes are listed in
[Lock verification outcomes](../adoption.md#lock-verification-outcomes). The resolver supplies exact validated `CatalogArtifacts` for
bootstrap; these contain only selected profiles/facets, never external fleet,
deployment or workstation configuration. Files with changed digests are rejected.

Adoption copies those exact pinned profile/facet bytes into the target catalog,
then verifies the target with the default local resolver. Subsequent adoption
can run without the original source bundle. It checks the complete prospective
catalog before writing: duplicate IDs, invalid entries, directory bounds and
symlinks fail. Existing differing files require explicit `--force`; matching
files remain unchanged. Baseline recording uses the same resolved audit limit.

Adoption keeps baseline recording enabled by default, including its dry-run debt
estimate. Recording creates a first baseline; an existing one is kept and checked
under the same limit ([the baseline on a re-adoption](../adoption.md#the-baseline-on-a-re-adoption)).
Both modes require verified local pins and their catalog, or an
explicit `--lock-source-root` for missing inputs. A dry run without these sources
fails instead of estimating debt with an unrelated default limit. Planning with
`--record-baseline=false` can explicitly omit that estimate; its report identifies
any unavailable policy verification.

`LoadEffectivePolicyContext` and `ResolvePolicy` are additive APIs. Existing
manifest parsing and runner hierarchy APIs remain available. The audit requires
materialized pinned sources by default and returns an error without them;
`--catalog-root` selects a matching source bundle.
