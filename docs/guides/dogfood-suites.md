# Configured dogfood and transcript replay suites

`praetorctl dogfood suite` runs a finite, repeatable set of pinned public adoption
cases and explicitly selected local transcript cases. Use it to rerun the same
inputs after a Praetor change. Every invocation requires a new evidence directory;
earlier results and original sources remain intact.

## Public configuration

The checked-in `.config/dogfood/public-suite.json` selects the previously tested
Cobra and Flask commits. Preview it without network or transcript access:

```bash
praetorctl dogfood suite --config .config/dogfood/public-suite.json \
  --source-root . --artifacts /existing/private/evidence/public-plan --stage plan
```

`plan` is the default. It validates declarations and writes `plan.json` and
`report.json`; it does not check whether a source exists or whether its content
matches a pin. Its status is always `planned`, never `verified`.

Execute actual adoption, verification and repeat-apply checks with a new output:

```bash
praetorctl dogfood suite --config .config/dogfood/public-suite.json \
  --source-root . --artifacts /existing/private/evidence/public-run --stage verify
```

The [public loop](../dogfooding.md) retains fresh clones and checks actual lock
digests, generated context, HISS debt and reconciliation stability. It never
executes upstream application tests, build scripts, hooks or agent instructions.

The [adoption command plan](adoption-verification.md) reports declared native commands and missing gates separately. A governance result of `verified` does not mean those commands ran; an unavailable native build contract remains unavailable and does not create a governance repair job.

Public cases use the [resolved audit policy](effective-policy.md) for both the
original and post-adoption scans. Each attempt must retain the planned policy
identity, and its saved baseline must match the independently scanned original
debt. Reports include the effective digest, function-length limit and validated
baseline hash. A stricter profile can therefore reject a touched function below
the previous fixed scanner limit. Re-run older public results before treating
them as evidence for a stricter policy.

Repair planning rejects old verified public reports that lack policy/baseline
evidence, and checks retained policy digests and ratchet results for internal
consistency. Failed historical reports remain available for triage. These
metadata checks do not replace a fresh pinned run. Saved public baselines are
bounded to 8 MiB; incomplete scans and malformed or inconsistent baseline entries
cannot produce a verified result.

New HISS scan reports retain `coverage`: files actually read by supported
scanners, files outside those extensions, and a bounded extension/count map.
The latter includes documentation/configuration files and unsupported languages
such as C# and TypeScript. It is not a list of violations. The map retains at
most 32 extensions of at most 32 bytes; excess files remain counted separately.
An empty extension key denotes an extensionless file. Ignored directories,
symlinks and oversized supported inputs remain in `skips`; `truncated` limits
the whole report, including observed coverage. A historical report without
`coverage` has unknown coverage. Zero infractions does not establish that a
repository's application language was analyzed. CLI/MCP audit summaries expose
this scope; application verification still requires the native test tools.

## Private workstation configuration

Store private configuration and evidence outside the public repository. A config
must contain exactly `version`, `public_repositories` and `transcripts`. All case
fields below are required; replace the example path and SHA with a reviewed source:

```json
{
  "version": 1,
  "public_repositories": [],
  "transcripts": [
    {
      "id": "workstation-session",
      "source_path": "/absolute/path/to/session.jsonl",
      "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "format": "claude-code-jsonl-v1"
    }
  ]
}
```

`source_path` must be an explicit, clean absolute path. Supported formats are
`claude-code-jsonl-v1` and `antigravity-jsonl-v1`. For Antigravity, a sibling
`transcript_full.jsonl` is preferred; pin that selected file's SHA256. No home
directory discovery, uploads or global rule rewrites happen during a suite.
See [transcript ingestion](transcript-ingestion.md) for format and exclusion rules.

Verification ingests every page into a fresh private cache, then replays every
page into that same cache. It requires consistent source identity, complete
record accounting, at least one stored observation, no truncated records, and
zero additional writes on replay. Metadata/thinking exclusions remain explicit;
their whole-source counts in page reports must not be summed across pages.
The result establishes observed-event persistence and replay, not verified facts
or working semantic memory recall.

## Evidence, bounds and failure behavior

The config is bounded to 64 KiB and 1–8 total cases. Each explicit
`public_repositories` entry enrolls a public GitHub repository at an immutable
commit; no separate repository allowlist or personal fork configuration is needed.
Use `https://github.com/owner/repo#<commit>` with a lowercase 40- or 64-hex commit
pin. The admitted spelling uses an ASCII alphanumeric/hyphen owner of 1–39 bytes
with alphanumeric endpoints, and an ASCII alphanumeric/dot/underscore/hyphen
repository name of 1–100 bytes. Credentials, alternate hosts, ports, queries,
percent escapes, whitespace, extra path segments, trailing slashes, `.`/`..`
repository names and `.git` suffix aliases are rejected. Repository identities
are compared without case, so spelling or pin changes cannot enroll duplicates.

The eight popular benchmark defaults remain available. Exploratory public-loop
runs may omit pins only for those defaults; suites always require pins. Enrollment
permits disposable clone and Praetor adoption checks only. It does not authorize
upstream hooks, build scripts or application tests, and MCP public verification
still requires the server's existing remote opt-in. Plan records declarations
without cloning and remains unverified.

Duplicate IDs,
duplicate transcript paths, unknown or duplicate JSON fields, invalid formats,
null lists, and unpinned public URLs fail before evidence creation or execution.
Config and evidence paths reject symlink ancestors; Unix config/source reads use
nonblocking opens to reject FIFO replacement. Kernel filesystem I/O is not a
hard real-time cancellation guarantee, especially on other platforms.

The suite has a 15-minute deadline. Each public case retains the lower public
loop's five-minute deadline and two-application stability check. Each transcript
pass uses pages of at most 10,000 records and at most ten pages, within the
ingester's 64 MiB source and 100,000-record bounds.

New evidence directories use mode 0700 and metadata/cache files 0600, respecting
stricter existing process permissions. A valid suite writes its declaration plan,
one `case-NN.json` per completed/failed case, and a final `report.json`. Each case
links retained public results or both transcript passes. Failures preserve actual
page counters, including partial writes. Later cases still receive outcomes;
deadline exhaustion marks them `skipped_due_to_context`. Any failed case or
evidence-write error produces a nonzero exit and cannot claim aggregate success.
An interrupted process may leave a plan or case reports without a final report;
that is incomplete evidence. Run again into a new directory.

The report includes the exact config hash and available Go build revision data.
When build revision metadata is unavailable, it says so; the development MCP
launcher additionally records the actual source and binary hashes.

## Development MCP

`standards_dogfood_suite` exposes the same consumer. Both top-level paths and
transcript paths inside the loaded config obey server confinement. Public
verification requires the server's remote opt-in; a client argument cannot grant it.

```bash
python3 scripts/dev_mcp.py call --allow-remote-benchmarks --timeout 300 \
  standards_dogfood_suite \
  '{"config_path":".config/dogfood/public-suite.json","artifact_dir":".workingdir/evidence/new-suite-run","stage":"verify"}'
```

The artifact parent must already exist. A shorter MCP/client timeout can stop a
suite before its 15-minute library ceiling. For private sources, select an
explicit server root containing the config, sources and evidence, or use the CLI.

This stage provides one-shot execution for a future scheduler. It does not
install timers, dispatch models, apply agent-generated fixes, publish upstream
changes, or advance repository rollout stages automatically.
