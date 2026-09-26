# Develop against this checkout's MCP

Run from the repository root with Python 3, Git, and the Go version required by
`go.mod` available on `PATH`:

```bash
python3 scripts/dev_mcp.py probe
```

This builds the current Go sources into a temporary binary, initializes its real
stdio MCP transport, and checks `serverInfo.version` against
`provenance.server_version` (`dev-<source_sha256>`). The report includes the checkout,
source and binary hashes, Git revision, dirty state, and Go version. Keep that
provenance with defect evidence. A source change during the build or probe fails
the run; retry against the completed edit.

The probe checks checkout symbol inspection, canonical context compilation with
all six outputs read back, verification drift, invalid arguments, path escape,
unknown tools, corrupt memory, and malformed locks. It also audits a valid fixture
before checking changed pinned content, invariant violations, and incomplete scans
that must fail without changing the baseline. All mutations use disposable
fixtures. A discovered tool name alone does not prove working behavior.

## Native project connections

The MCP server is client-neutral at the wire boundary. Client configuration and
lifecycle behavior are separate acceptance surfaces. The supported projections
are Codex, Claude, Gemini, OpenCode v1, Continue, Cline, Kilo, and AGY; see
[agent lifecycle coverage](agent-lifecycle.md) for their current qualification
states. A projection test or checked-in client file does not prove that an
installed client loaded, trusted, reconnected, or used the server.

Refresh the regular commands on this workstation with `make dev-install`.
`make build` writes only to the checkout's `bin/` directory; it does not update
standalone executables already on `PATH`. `scripts/dev_install.py` runs the MCP
behavior probe against a fresh build, then delegates the atomic install itself to
`praetorctl workstation install` (HISS-19: one installer, not two): it builds all
three Praetor binaries from current source and installs them into `~/.local/bin`
with the three legacy aliases. `python3 scripts/dev_install.py --help` lists
destination overrides for isolated installations.

`workstation install` rejects an unexpected symlink or special file at an
installation target before touching anything, backs up whatever is already
there, reads installed hashes back, and restores on a caught installation
error. It serializes concurrent installers through a lock directory in the
destination and records the install (including the prior commit and backup
location, once there is one) in a per-user install manifest. See
[workstation install and status](workstation-update.md) for the manifest
location, the lock and rollback behavior, and `workstation status`.
Already running CLI/MCP processes continue using their original executable until
restarted. The development connection below still builds directly from source.

The tracked configs register `praetor-dev`; they contain no machine-specific paths
or credentials. Both run `scripts/dev_mcp.py serve`, which builds once per server
startup. Existing native connections keep that binary after source edits. Restart
or reconnect the server before testing the new code, or use the fresh direct
commands below. Check the server's initialization version against a fresh probe's
`provenance.server_version`; a matching Git commit alone misses uncommitted edits.

**Codex:** open this trusted checkout in Codex. Its project `.codex/config.toml`
registers the server with 120-second startup and tool timeouts. The launcher resolves
the Git worktree root from the session directory, so nested starts work. Verify the
loaded configuration with `codex mcp get praetor-dev --json`; use `/mcp` to inspect
the connection. Restart the CLI session or IDE extension to load a new server.
Project config and these MCP options are documented in the
[Codex MCP reference](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

**Claude Code:** open the checkout and enable its project `.mcp.json` server when
Claude presents the project approval. Check `claude mcp get praetor-dev` and use
`/mcp` to reconnect after edits. The launcher expands `CLAUDE_PROJECT_DIR` inside
the child shell; this documented server environment variable avoids depending on
Claude's launch directory. Allow cold Go builds with `MCP_TIMEOUT=120000 claude`;
the checked-in per-tool timeout is 120 seconds. See the
[Claude Code MCP reference](https://code.claude.com/docs/en/mcp).

Configuration parsing was checked with Codex CLI 0.145.0 and Claude Code 2.1.259.
Codex's real app-server tool-call path was checked from the repository root and a
nested directory. Claude's config was recognized as project-scoped, and its exact
launcher completed a handshake and symbol read from an unrelated directory with
the documented child environment. Its native connection remains subject to
Claude's project approval. If a client cannot load the project connection, record
that limitation and use the direct wire client.

**Other supported clients:** use `praetorctl clients prepare` or the documented
native plan for Gemini, OpenCode v1, Continue, Cline, Kilo, or AGY. Inspect the
exact destination or argv, complete that client's approval/reload flow, and
perform the initialization, discovery, and harmless readback checks. Existing
projection tests establish serialization and conflict handling; they are not
live session evidence.

Gemini's checked-in `.gemini/settings.json` also contains the native
`BeforeTool`/`AfterTool`/`AfterAgent` lifecycle hooks. Claude's
`.claude/settings.json` contains the corresponding `PreToolUse`/`PostToolUse`/
`Stop` hooks. OpenCode v1, Continue, Cline, Kilo, and AGY have no checked-in
Praetor lifecycle adapter; their MCP projection does not imply lifecycle
enforcement. Cursor, Windsurf, and Copilot have compiled context support only.
See [agent lifecycle coverage](agent-lifecycle.md) for the complete matrix.

## Reproduce through a real tool

Each direct call fresh-builds the checkout, verifies the server identity, and
prints its JSON-RPC response with provenance. Tool errors and protocol errors
produce a nonzero exit code:

```bash
python3 scripts/dev_mcp.py call standards_inspect_symbols \
  '{"path":"cmd/standards-mcp/main.go"}'
```

Use a temporary server root for mutation cases. `--root` follows the subcommand
and changes the tool's confined filesystem root; the code still comes from this
checkout. This example checks a real write, readback, and verification:

```bash
python3 - <<'PY'
import json
from pathlib import Path
import subprocess
import tempfile

with tempfile.TemporaryDirectory(prefix="praetor-mcp-case-") as directory:
    root = Path(directory)
    (root / "AGENTS.md").write_text("# Fixture\n\nPreserve fixture-policy.\n")
    command = ["python3", "scripts/dev_mcp.py", "call", "--root", directory,
               "standards_compile_context"]
    subprocess.run(command + ["{}"], check=True)
    assert "fixture-policy" in (root / "CLAUDE.md").read_text()
    subprocess.run(command + [json.dumps({"verify_only": True})], check=True)
PY
```

The writing call also splices the [text register](text-register.md) block into the
fixture's `AGENTS.md`, rendered from the manifest beside it, before the vendor files are
compiled. A `verify_only` call never writes the source: it reports a stale or missing block
as `Context verification failed`, and `standards_audit` reports it in its context gate.
Both also run the caveman lint over `AGENTS.md`, as `praetorctl compile-context --verify`
does: prose there fails with `AGENTS.md fails the caveman lint` and the first findings, and a
pass names its counts (see [the context gate](text-register.md#the-context-gate)). The
fixture above is terse, so it passes.

For each defect, retain the input, expected behavior, actual response, provenance,
and filesystem evidence. Exercise positive, negative, and boundary behavior of the
affected tool. Report placeholder results, missing operations, and blocked
connections explicitly; the smoke probe does not establish correctness of every
tool. Run the relevant code tests and `make verify-all` after implementing the fix.

### Symbol inspection judges against the repository's ceilings

`standards_inspect_symbols` prints each Go function's lines, statements,
cyclomatic and cognitive complexity beside the ceiling it is judged against, as
`LOC: 43 (<=60)`, and marks a function over any ceiling `HISS-04 WARN: <bounds>`.
The ceilings come from `config.ResolveRepositoryComplexity`
(`internal/config/repository_policy.go`) for the server root, the resolver
`praetorctl editors` and `standards-lsp` also use, so the three report the
ceilings `praetorctl audit` enforces in that repository:

- A locked repository reports its resolved policy: pinned profiles, repository
  overrides and the audit function-length cap (`config.AuditMaxFuncLOC`, 60).
  That can be looser or tighter than the HISS-04 figures in `AGENTS.md`.
- No `.standards.yaml`, or a manifest without `.standards.lock`, reports the
  HISS-04 ceiling (cyclomatic 10, cognitive 15, statements 50, 60 lines),
  tightened by any complexity override the manifest declares.
- Every 60 above is `hiss.DefaultMaxFuncLOC` (`internal/hiss/hiss.go`), the
  scanner's own default and the one constant every function-length default
  derives from; `TestServer_Boundary_InspectSymbolsLengthIsScannerDefault` in
  `cmd/standards-mcp/server_test.go` pins the locked and unadopted cases to it.
- A manifest or lock that does not resolve, including the lock `praetorctl init`
  writes, reports that same ceiling and opens with
  `[WARN] repository policy unresolved (<cause>); stating the HISS-04 ceiling ...`.
  The inspection still runs; `praetorctl audit` still fails on that state.
- Policy resolution fails the tool only when the request is cancelled or the
  resolution times out: `Inspection failed: resolve complexity policy for <root>: ...`.

[Complexity ceilings](editor-capabilities.md#complexity-ceilings) explains why the
lock-less case differs from `praetorctl plan`. Tests:
`TestServer_Boundary_InspectSymbolsFollowsRepositoryPolicy` and
`TestServer_Negative_InspectSymbolsUnresolvablePolicy` in
`cmd/standards-mcp/server_test.go`.

`standards_plan` opens with `=== Praetor Reconcile Plan (Dry Run) ===` and takes
the `Repository: <owner>/<name>` line from the manifest's `repository` block
(`TestWritePlanHeader_NamesTheManifestRepository`). Its target invariants are the
built-in defaults plus the manifest's overrides (`createPlanTool` in
`cmd/standards-mcp/server.go`); it does not read `.standards.lock`.

### Shared audit authority and parity

The `standards_audit` tool executes the same gates as CLI `standardsctl audit`.
Both tools share the same authority implementations for artifact checks. When
verifying branch protection rulesets, `standards_audit` consults
`adopt.AuditBranchProtection`. If `.standards.yaml` records an accepted
`adoption.decline: [branch-ruleset]`, `standards_audit` reports a verified pass
(`[PASS] Branch protection ruleset declined by adoption.decline.`) rather than
failing on the absent `.github/rulesets/main.json`. When not declined and required
by policy, `standards_audit` fails closed if the ruleset file is missing or invalid,
matching CLI behavior with byte-for-byte verdict parity.

The lock digest gate inside `standards_audit` resolves its catalog from the same
`catalog_root` tool argument the effective-policy gate uses
(`p.policy.CatalogRoot` in `cmd/standards-mcp/audit_tools.go`), not the repository
root by default. It calls `config.ValidateLockfileWithOptions` with
`RequireSources: true`, so a lockfile with no catalog to hash against fails the
tool call (`[FAIL] lockfile content digests are unverifiable: ...`,
`config.ErrLockUnverifiable`) instead of reporting a pass. A catalog that exists
but no longer defines a pinned archetype's `id:` fails the same way with
`config.ErrLockSourceMissing`. See
[lock verification outcomes](../adoption.md#lock-verification-outcomes) for the
full outcome table shared with the CLI.

## Retained public dogfood loops

Use the [public dogfooding guide](../dogfooding.md) for the shared CLI/MCP
plan/apply/recheck loop. Public cloning is disabled by default. Explicitly add
`--allow-remote-benchmarks` to `serve` or `call`; a client tool argument cannot
turn on network access. Direct calls can use `--timeout 300` for the bounded
five-minute loop. Keep `artifact_dir` under the confined server root, such as
`.workingdir/evidence/public-dogfood`, and retain the JSON-RPC envelope with its source identity.

For recurring suites, use the [user timer guide](scheduled-dogfood.md).
`standards_dogfood_schedule_status` reads a configured schedule and retained
attempt state. The configured runner binary, suite, source bundle, state, and
repair-policy paths must all fit the server root. Status cannot run suites,
dispatch agents, or create state. It hashes the configured CLI executable so
MCP and CLI status agree even though they are separate processes.

For configured repair execution, use the [repair guide](dogfood-repairs.md).
`standards_dogfood_repair_status` reads a retained suite report and execution state
through a confined private configuration. Probe it with an actual failed suite
report, then verify that status leaves both the report and absent execution state
unchanged. It never invokes credential helpers or providers. Only the explicit
`praetorctl dogfood repairs run` command starts an execution attempt.

The private execution configuration must pin `source_sha` to a commit that exists
in the configured source repository and carry the canonical register tuple and
manifest digest for that snapshot. Repair status rejects stale or caller-selected
provenance before it reports retained execution state.

## Rule explanations state when a rule is not enforced

`explain_rule` answers for every HISS identifier, including the ones with no executable check. Those
answers say so explicitly:

```text
Rule: HISS-05 (Variable Scoping)
Enforcement: NOT ENFORCED. No executable check exists in this repository; the matrix names a
linter that is not configured for this rule.
Failure Action: None today; the rule is advisory until a check is attached.
```

An agent asking about a rule needs to know whether anything will stop it. Returning a formal
specification with no enforcement note reads as a gate that exists, which is the defect the HISS-20
coverage catalog was built to remove — a declared mechanism nothing implements. The same honesty
applies here: the tool reports the rule *and* whether it bites.
