# Capability discovery

`praetorctl dogfood discover` observes whether a selected Praetor capability
adapter is available in a repository. It compares the configured Praetor catalog
signals with immutable file evidence. It does not discover arbitrary upstream
product features, run application builds or tests, inspect runtime behavior, or
classify an upstream repository’s missing feature as an upstream bug.

## Inputs and commands

Use one explicit local path or one pinned public cohort:

```bash
praetorctl dogfood discover --path /private/repo \
  --policy .config/dogfood/discovery-policy.json \
  --artifacts /private/evidence/discovery-plan --stage plan
praetorctl dogfood discover --path /private/repo \
  --policy .config/dogfood/discovery-policy.json \
  --artifacts /private/evidence/discovery-observe --stage observe
```

The public cohort uses the same observer and is configured by
`.config/dogfood/discovery/public-100.json`:

```bash
praetorctl dogfood discover --config .config/dogfood/discovery/public-100.json \
  --policy .config/dogfood/discovery-policy.json \
  --artifacts /private/evidence/public-discovery --stage observe
```

`--path` and `--config` are mutually exclusive. Public observation requires
explicit remote opt-in through the MCP server. The CLI’s explicit public
cohort mode supplies that authorization for its pinned inputs. Sources are
cloned at immutable commit pins, read through a bounded snapshot, and never
receive hooks, messages, repairs, or upstream command execution.

The MCP tool is `standards_dogfood_discover`. Its required arguments are
`policy_path` and `artifact_dir`; provide exactly one of `path` or `config_path`.
`stage` is `plan` or `observe`. The tool returns a compact summary and retains
the detailed report under the requested private artifact directory.

## Policy and evidence

The policy is version 1 JSON with one to 128 rules:

```json
{"version":1,"rules":[
  {"key":"hiss:typescript","title":"TypeScript scanner",
   "kind":"scanner_extension","matches":[".ts",".tsx"],"analyzer":"hiss"},
  {"key":"needs:dotnet","title":".NET analyzer",
   "kind":"needs_marker","matches":["*.csproj"],"analyzer":"dotnet"},
  {"key":"verification:project","title":"Project plan",
   "kind":"verification_marker","matches":["Makefile"],"analyzer":"adopt"}
]}
```

Rules have one stable key and one to 32 exact extensions or marker patterns.
Scanner extensions are compared with the actual file extension and HISS
dispatch table. Marker rules match an exact basename or `*.extension` and are
evaluated once per bounded project root. Evidence records relative paths and
content hashes; at most 32 hashes are retained per observation while
`evidence_count` preserves the full bounded occurrence count.

Observation statuses are `available`, `unsupported`, and `unknown`.
`unsupported` means the selected adapter cannot inspect the observed language
or marker and is eligible only for a review candidate. `unknown` covers missing,
ambiguous, malformed, truncated, or failed planning input. A marker with no
matching file is `not_applicable` in its basis and does not create a candidate.
Adoption verification plans remain declarative; ambiguous project commands stay
unknown until a separate, explicitly permitted verification run.

The case evidence includes `tree_sha256`, `files_observed`,
`files_matched`, and per-rule observations. The aggregate report always has
`verified: false`. A changed source
snapshot fails stability readback and admits no candidate. Incomplete cases are
excluded from ranking. No report status means application readiness.

## Bounded review loop

The review loop is: run `plan`, run `observe`, rank deduplicated unsupported
observations by stable capability key and repository recurrence, review the
candidate against source-backed requirements, implement or configure the
adapter, then replay the same pinned/local input. After the replay is stable,
run the selected full dogfood suite separately. Candidate recurrence prioritizes
review; it is not proof of duplicate implementation, upstream defect, or release
readiness.

The discovery run admits at most 128 repositories and four workers within a
15-minute run. Public cloning has a two-minute deadline and each case has a
three-minute observation deadline. Snapshots allow at most 20,000 entries,
16 MiB per file, and 256 MiB total. The clone deadline is a time bound, not a
byte-cap claim. Ignored Praetor state, generated trees, dependencies, and
symlinks are excluded from source evidence. Keep local repository paths and
artifacts in private ignored storage.

The [first 100-repository observation](../research/dogfood-capability-discovery.md)
records candidate recurrence and incomplete cases. Discovery is an explicit
stage today; it does not start a scheduler or automatically implement candidates.
