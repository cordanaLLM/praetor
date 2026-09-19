# Local Git hooks

Install Lefthook 2.1.12 or newer, then run `make hooks` and `make hooks-check`.
The configuration is tested with 2.1.12. Python 3, Git and the repository Go
version are required. Install `yamllint`, `shellcheck`, `actionlint` and `hadolint`
when editing their file types; applicable checks fail if their tool is missing.
Strict source pushes also require `gosec`, `govulncheck` and `semgrep`. Golangci-lint runs
from source using the existing repository `@latest` policy.

`make verify-all` also runs `make semgrep-test`, which checks the real matcher on
temporary positive, negative and boundary fixtures. The engine version comes from
`.config/semgrep/requirements.txt`; missing or mismatched engines fail the check.
This is rule regression coverage, not a replacement for repository source scans.
For an isolated installation, use the tested 1.177.0 release (Python 3.14 locally):

```bash
PRAETOR_TOOL_DIR="$HOME/.local/share/praetor-tools/semgrep-1.177.0"
python3 -m venv "$PRAETOR_TOOL_DIR"
"$PRAETOR_TOOL_DIR/bin/python" -m pip install -r .config/semgrep/requirements.txt
mkdir -p "$HOME/.local/bin"
ln -s "$PRAETOR_TOOL_DIR/bin/semgrep" "$HOME/.local/bin/semgrep"
semgrep --version
```

Keep `$HOME/.local/bin` on `PATH`. The symlink command deliberately refuses to
replace an existing executable. See the [Semgrep package installation guidance](https://pypi.org/project/semgrep/1.177.0/).

| Git stage | Work performed |
| --- | --- |
| `pre-commit`, `pre-merge-commit` | Audit the live private ledger, then check the exact index for whitespace, conflict markers, Python/JSON syntax, YAML, shell, workflow and Docker lint; Go formatting and vet on changed packages; verify affected generated agent instructions. |
| `prepare-commit-msg` | Add an instructional comment to a fresh empty message. |
| `commit-msg` | Verify the live state synchronization after Lefthook restores partially staged worktree files, then require a conventional subject and DCO sign-off; accept Git merge/revert subjects. |
| `pre-push` | Audit and verify live state before inspecting each actual pushed commit. `checkpoint/*` destinations run file checks, affected Go builds and race tests. Other destinations also require lint, security, vulnerability, governance, flavor and signed-receipt gates. |
| `post-commit` | Synchronize and read back the private state for the new commit, then print the dedupe cadence reminder when due. Sync failures are reported. |
| `post-checkout`, `post-merge`, `post-rewrite` | Warm changed module dependencies in an isolated clone, rebuild the local CLI for source changes, verify affected agent outputs, report governance changes. File-only checkouts do nothing. |
| `pre-rebase` | Check the hook environment before replay begins. |

Pre-commit exports the index into a temporary directory. Unstaged edits, untracked
files and the index remain unchanged. Formatting is a read-only gate: run `gofmt`
and stage your chosen hunks explicitly. The export pins `core.autocrlf=false`, so
the isolated checks inspect the bytes in the index instead of rewriting them to the
operator checkout's line endings. Deleted files are included when computing
scope and skipped by per-file linters. Paths are read with NUL delimiters and
passed as process arguments, including filenames containing spaces or shell text.
A missing required tool or a failed subprocess blocks the operation.

`yamllint` covers every staged `.yml` and `.yaml` file except a Helm chart template: a file
at any depth under a `templates/` directory whose chart root holds `Chart.yaml`. Helm renders
`templates/` recursively, so `templates/rbac/role.yaml` is as much a template as
`templates/service.yaml`. Those files are Go templates that render YAML, so a YAML parser
rejects `{{- if }}` before a single rule can run. The chart's own `Chart.yaml` and
`values.yaml` are ordinary documents and stay linted, and a `templates/` directory with no
`Chart.yaml` beside it is not a chart at all.

The entire `/.workingdir/` directory is private, Git-ignored workstation state.
Git metadata checks reject staged additions and changes beneath it, including
forced staging and submodule entries, before exporting the index. Push checks
inspect every outgoing commit, so adding a private file and removing it in a later
commit still blocks publication. The history scan is bounded to 1,000 commits;
an unknown baseline selects the full reachable history, and exhaustion fails.
Removing previously tracked entries is allowed and preserves local
files when done with `git rm --cached`. Previously published content remains in Git
history. Put cluster connection guides and backend notes in this ignored directory;
publish only reviewed, sanitized documentation under `docs/`.
The root `.dockerignore` also excludes this directory and Git history from local
container builds; Docker applies its [build-context ignore rules](https://docs.docker.com/build/concepts/context/#dockerignore-files)
separately from Git.

## What a hook run prints

A hook run prints the output of its jobs and, when a job fails, Lefthook's exit
status plus one `✗ <job>` line naming it. There is no version banner, summary block,
per-job success line or color. `.config/lefthook/praetor.yml` sets this with
`output: [execution_out, failure]` and `colors: false`.

Every run is read by an agent: the hooks fire on each commit, checkout and push and
on agent lifecycle events, for the main session and for every subagent. Measured
with Lefthook 2.1.12 on the same inputs, before and after the policy:

| Run | Characters before | Characters after | Escape sequences before |
| --- | ---: | ---: | ---: |
| `agent-pre-tool`, allowed command | 1,868 | 27 | 106 |
| `agent-pre-tool`, blocked command | 2,074 | 266 | 106 |
| `agent-checkpoint-tool` | 2,379 | 319 | 120 |
| `agent-state-stop` | 2,092 | 185 | 110 |
| `pre-commit`, one staged file | 1,857 | 158 | 98 |

None of these runs prints an escape sequence after the change. The markers the native adapters
read (`PRAETOR_COMMAND_POLICY_OK`, `PRAETOR_CHECKPOINT_SCOPE_OK`,
`PRAETOR_CHECKPOINT_RESULT=`, `PRAETOR_STATE_RESULT=`) are job output, so they are
printed exactly as before. `test_hook_output_is_job_output_and_failures_only` in
`.config/lefthook/scripts/test_hooks.py` pins both halves: a pass prints only its
marker, and a blocked command still prints its reason and the failed job's name.

For Lefthook's full reporting on one run, set `LEFTHOOK_OUTPUT`, for example
`LEFTHOOK_OUTPUT=meta,summary,execution git commit -s`. The
[pinned Lefthook `output` reference](https://github.com/evilmartians/lefthook/blob/v2.1.12/docs/configuration/output.md)
lists the values. A personal `lefthook-local.yml` can set `output` or `colors`; it is
loaded after `extends` and wins.

## Remote checkpoints

The owner approved a separate `checkpoint/*` namespace for unfinished audit work.
Pushes to it require the same snapshot file checks plus affected Go builds and
race tests. Full CI runs on every checkpoint push; a checkpoint is a WIP backup,
not a passing verification receipt or permission to merge. Fixes can therefore be
shared while the remaining repository audit findings stay visible in CI.

```bash
git switch -c checkpoint/my-task
git push -u origin checkpoint/my-task
```

Only the actual destination under `refs/heads/checkpoint/` selects this policy.
Other branches, tags, and PR/merge gates retain their strict checks. A push that
contains both checkpoint and strict refs must pass both policies, even when they
point at the same commit. No environment flag disables a gate.

The separately configured [review policy](review-policy.md) supports explicit
single-maintainer operation while another reviewer is unavailable. This changes
the hosted approval requirement; the Git hook and verification gates still run.

Pre-push consumes Git's ref protocol once and checks disposable clones of those
commit IDs. It handles multiple refs, new branches, tag targets, deletions and
pushes that do not use the current checkout. An existing branch uses the remote
OID supplied by Git; a new branch first uses the known remote default branch's
ancestor, then other eligible remote ancestors. Strict pushes exclude checkpoint
baselines. Missing ancestry or an unavailable remote object selects a
conservative full-tree check. Empty input and ref deletions have no new content
to validate.

The stdin-consuming pre-push entry is a Lefthook script job. It runs even when
Lefthook estimates an empty final file diff, so intermediate commits that add
and then remove private content still reach the history check.

Go scope includes changed packages, test-only reverse dependencies, testdata and
embedded inputs of any extension. Module/build/security configuration selects all
packages. Deleted files beneath a package with embed patterns conservatively
select that package and its dependents. Go's package metadata is inspected before
skipping documentation, so embedded Markdown is still tested. Ordinary docs and
state edits skip Go race/security gates. A root `README.md` edit still runs the
governance and flavor audits, so a stale or malformed managed governance block
cannot pass through the lightweight documentation path. Governance and flavor
audits also run for source and policy/configuration edits. The live private ledger is checked outside
the exported snapshot; it never becomes published snapshot content. Source and
governance pushes also retain the full `gate run` pipeline and pinned-key
`gate verify`. Its full race/security stages replace duplicate scoped runs;
golangci-lint still uses affected packages. Verified receipts are retained under
Git metadata at `praetor-receipts/<commit>.json`, without staging an artifact. Existing
P0 blockers, audit failures, zero-warning lint failures and all gosec rules remain
enforced. The full gate and CI remain the final integration checks.

The hook bounds the `gate run` and `gate verify` subprocesses by the gate's own run deadline,
not by a figure of its own. `run_full_gate` in
[`checks.py`](../../.config/lefthook/scripts/checks.py) first runs `gate deadline --json`, which
resolves `PRAETOR_TEST_STAGE_TIMEOUT` through the pipeline's parser: the race-stage bound, clamped
to its 30-minute ceiling, plus the allowance for the other stages. It then adds a two-minute
launch margin (`GATE_LAUNCH_MARGIN`) for `go run` to rebuild the CLI. With the variable unset that
is 8 + 2 = 10 minutes, the value the hook used to hard-code; at the ceiling it is 37 minutes. The
hook previously stopped the gate at a fixed 600 seconds, below the documented ceiling, so a raised
bound could never take effect on a push (#314). A deadline report the hook cannot use fails the
push rather than running the gate under a guessed bound. The deadline itself is described in
[adoption verification](adoption-verification.md#the-whole-runs-deadline).

When that bound expires, or Ctrl-C interrupts the push, the hook stops the gate before it kills
it. `stop_process_group` in [`common.py`](../../.config/lefthook/scripts/common.py) sends the
child's process group SIGTERM (SIGINT for Ctrl-C), waits up to `STOP_GRACE` (10 seconds) for every
member to exit, and only then sends SIGKILL; a second Ctrl-C during the wait kills at once. The
CLI runs each git and go command in a process group of its own and forwards a catchable signal to
those groups, waiting up to 5 seconds for them (`internal/util/command_interrupt_unix.go`), so git
removes its index lock and nothing is left running. SIGKILL on the CLI's group cannot be forwarded
and misses those groups. On Linux the kernel still kills each command when the CLI dies
(`internal/util/command_parent_death_linux.go`); the processes a command started, and every
command on macOS, would run on. The `test_stop_*` cases in
[`test_hooks.py`](../../.config/lefthook/scripts/test_hooks.py) replay the forwarded stop, the kill
after the grace, and the second Ctrl-C.

Before snapshot governance checks, the disposable clone initializes its own
missing private ledger and audits it. Incomplete or invalid existing state still
fails. The live ledger is never copied into the clone.

Snapshot Git reads ignore configuration injected through the environment:
`GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>`, `GIT_CONFIG_VALUE_<n>` and
`GIT_CONFIG_PARAMETERS` (what `git -c key=value push` sets) are removed, so a
one-off setting cannot change the bytes a gate checks. Commit-time exports keep
`GIT_INDEX_FILE`, `GIT_DIR` and `GIT_WORK_TREE` as Git set them, so
`git commit -a` and `git commit -- <path>` are checked against the index they
record, not the stale `.git/index`. Ref-backed snapshots use a fully clean
environment. `.config/lefthook/scripts/common.py` (`index_env`, `clean_env`)
implements this, and `.config/lefthook/scripts/test_hooks.py` covers both paths.

After reviewing and staging your work, run `praetorctl state sync .`, then
`git commit -s -m 'fix(scope): describe the change'`. Task, bug, question, staged,
unstaged or untracked input changes require another sync. The hook does not create
a DCO attestation or task narrative on your behalf. For a merge that changes the
index, use `git merge --no-commit`, inspect the result, sync, then commit. Run the
required `praetorctl state sync .` at turn end; keep the ledger local and untracked.
`make state-audit` initializes a missing ledger in a fresh checkout, then audits it;
existing incomplete, malformed, or P0-blocked state still fails. Post hooks cannot
undo an operation that already succeeded;
resolve any reported refresh failure before continuing.

`praetorctl state sync --verify .` is read-only. It requires a versioned SHA-256
binding in the final STATE entry covering its complete preceding history, the
absolute worktree root, branch and full HEAD (or explicitly verified unborn
branch), index entries, staged and unstaged binary Git diffs, visible Git status,
untracked file bytes, and OPEN, BACKLOG, BUGS and QUESTIONS bytes. STATE records
the observed Git state, clean flag and dirty count. `state status` reports an
inspection time, never a fabricated last-sync timestamp.

Only these canonical ledgers are read inside `.workingdir`; evidence, memories
and caches are excluded. Git-ignored public paths are also outside the binding.
Limits are 1 MiB per canonical ledger or untracked file, 8 MiB total untracked
bytes, 10,000 index entries/untracked paths and 8 MiB per Git output stream.
Git probes have five-second deadlines. Missing or oversized inputs, unsupported
submodules, assume-unchanged/skip-worktree entries, and configured Git clean or
process filters fail explicitly. Symlinked untracked files are rejected. These
checks establish synchronized bytes; they cannot establish that an agent recorded
every relevant task or explanation.

This repository's native Stop/AfterAgent bridge calls the shared `agent-state-stop`
job and requires its unique success marker. Missing, stale or invalid state blocks
completion. The Git hooks rebuild the checkout CLI before ledger checks to avoid
accepting an older installed binary. Generated adoption bundles require their own
compatible CLI and hook integration; this repository's activation is not evidence
that every adopted client enforces state freshness.

Useful local commands:

```bash
make check-staged
make changed-packages BASE=origin/main
make test-changed BASE=origin/main
make lint-changed BASE=origin/main
make sec-changed BASE=origin/main
make check-changed BASE=origin/main
make hooks-test
make verify-all
make sandbox-verify REF=HEAD
```

The `*-changed` targets compare committed `HEAD` against `BASE` and use exactly
the same scope and process runner as pre-push. `make verify-all` retains the full
repository checks. Independent checks run with at most three workers; command
failures are collected and propagated.

CI sets `TEST_COVERPROFILE` to an explicit temporary file and obtains coverage
from the race run inside `make verify-all`. The same run must meet the 65%
statement-coverage floor; CI does not execute a second full race suite.

The sandbox runner requires Docker and the explicitly selected local
`praetor-dev:audit` image. Override its name with `PRAETOR_SANDBOX_IMAGE`. It clones
an exact commit, creates a private home/cache, runs as the invoking UID, passes
`CI=true`, uses readonly module mode and removes the clone afterward. It never
copies uncommitted changes or silently ignores snapshot failures. Enable the
additional full sandbox gate on each pushed ref with `PRAETOR_HOOK_SANDBOX=1`.
It is opt-in because the existing development image may lack required tools;
`make sandbox-verify` fails clearly when the image or a gate is unavailable. A
successful documentation-only push does not generate a receipt. A source or
governance push requires the verified receipt, but does not imply the optional
Docker sandbox ran. Existing audit failures, missing scanners or missing signing
configuration remain blocking failures; they are not bypassed during this audit.

The command guard accepts actual PreToolUse JSON or command arguments; Git calls
its explicit environment mode. It rejects verification-evasion commands and
hook exclusions. A hook cannot intercept a Git invocation that disables all
hooks: repository protections and CI remain authoritative. Local configuration
must not disable required gates.

Hook-policy changes run `hooks-test` as part of their staged/push file checks.
The behavioral suite uses disposable repositories with installed Lefthook and
real commits/pushes. It includes staged/unstaged isolation, arbitrary filenames,
empty and deletion-only changes, malformed syntax/messages/protocols, negative
vet/race controls, embedded-input and reverse-dependency scope, governance gate
selection and every configured stage. It never disables the user's hooks.

## Native Codex command guard

The Git hook policy above is shared by humans and coding agents. This section
describes the separate native Codex adapter; it is not coverage for every tool
host or client. Other agents can invoke the shared Lefthook policy explicitly
and need a client-specific native adapter before claiming lifecycle coverage.

Git hooks run when Codex invokes Git, just as they do for a human. They do not
inspect every tool call or run verification at the end of a conversation turn.
The repository's `.codex/hooks.json` adds a separate `PreToolUse` handler for
Codex's native `Bash` tool. Specialized tool hosts and existing sessions require
separate runtime verification; this file does not establish their coverage.
The handler invokes `codex_pre_tool.py`, which executes
`lefthook run agent-pre-tool --no-tty --no-auto-install`. That required job runs
`block_evasion.py`. The adapter requires its success marker as well as exit zero;
a missing binary, missing job or skipped job blocks execution. Automatic Git
hook installation is disabled for this tool event only; install Git hooks with
`make hooks`. Git's hook driver calls the policy's environment mode;
Codex supplies the proposed shell command as JSON before execution.

The adapter translates rejected commands, invalid input, missing guard execution
and subprocess failures into Codex's blocking exit code 2. Input is capped at
1 MiB, guard execution at ten seconds, and diagnostics at 4 KiB. This is a
screen for known verification-evasion and topology patterns, not a complete
shell parser or an immutable security boundary. It does not inspect file edits,
arbitrary MCP calls, or later input sent to an already-running shell.

This integration is checked against Codex CLI 0.145.0 and Lefthook 2.1.12.
Version 2 supports custom agent lifecycle jobs; the earlier 1.13.6 validator
rejected them. CI installs the same pinned v2 release. The adapter translates
Lefthook's failure into Codex's blocking exit code rather than assuming their
exit semantics match. See the [upstream agent integration](https://lefthook.dev/configuration/ai/).

After opening this trusted repository in Codex, use `/hooks` to review and trust
the repository's `PreToolUse` definition. If it is not listed in an existing
session, start a new session from the repository. New or changed definitions are
skipped until trusted. User-level Hindsight hooks load alongside this handler.
The adapter's subprocess tests establish its behavior; activation additionally
requires a native Codex hook event after trust. A checked-in hook file alone is
not evidence that the current session is enforcing it.

The repository now configures PostToolUse and Stop checkpoint hooks. They share
Lefthook's checkpoint evaluator and require reviewed public changes to reach a
commit, gated push and draft PR according to the configured policy. They do not
replace `make verify-all` or certify the code themselves. See the
[checkpoint guide](checkpoint-cadence.md) for due results, local-only settings,
bounded continuation and activation evidence. Current-session interception still
requires a real native hook event; configuration alone does not establish it.

Native bootstrap can also be inspected without a model request:

```bash
python3 -B scripts/dev_codex_hooks.py status
python3 -B scripts/dev_codex_hooks.py trust --key '<reviewed project hook key>' --expected-hash 'sha256:<reviewed definition hash>'
```

Review the listed command and its referenced source before supplying its hash.
The bootstrap uses Codex 0.145.0's native `hooks/list` and `config/batchWrite`
operations, matching the native trust UI. It only updates the selected enabled
project command hook, rejects stale hashes and discovery warnings, and verifies
trusted status afterward. It preserves other hook and client settings. It does
not start an agent or reload an already-running IDE session. A definition hash
binds the command configuration, not the contents of a referenced script; source
review and the repository gates are still required after implementation changes.

The client shares MCP's bounded stdio transport, including deadlines, byte and
request limits, and child-process cleanup. Tests cover stale/foreign/duplicate
selection, failed discovery and readback, and real native-protocol pipe exchange.

## What a push scans, and against what

A push of an existing branch is scanned against the ref it is replacing. A push of a **new**
branch has no such ref, so the base is the merge-base with the remote's default branch, obtained
from `git ls-remote --symref <remote> HEAD`.

The remote is asked rather than `refs/remotes/<remote>/HEAD` being trusted. Git writes that
symref once at clone time and never updates it: it is inherited when a clone is cloned,
`git remote set-head` can point it anywhere, and nothing resets it when the remote's default
changes. Trusting it made the scan scope depend on a local ref nobody set deliberately — in one
checkout it named a feature branch, the base resolved six merges stale, and the staged scan grew
from 3 files to 36. That was enough for `semgrep-core` to exceed the host's `memlock` limit and
refuse the push with `Cannot allocate memory io_uring_queue_init`, an error naming memory, the
kernel and semgrep, and never the scope.

A push is already a network operation, so asking costs nothing new. An unreachable remote falls
back to the local symref rather than failing the push, and says so:

```text
Push baseline: remote default unavailable; using local refs/remotes/origin/main
```

Every push then prints what it is about to scan and the base that produced it:

```text
Push scope: 3 file(s) versus 028dad44db90
```

That line exists so a wrong scope reads as a wrong number rather than as whatever the scanner
does when handed too much work.

## Fixture inputs are excluded from source scans

`is_fixture()` in `.config/lefthook/scripts/checks.py` treats any path under a `testdata/`
directory as input to the rules rather than source governed by them, and the gofmt and semgrep
selections skip it.

This is not a convenience. The HISS-20 corpus under `.config/hiss/testdata/` exists *because* its
positive fixtures violate an invariant — a file that fails to be a bounded loop is how the rule is
proven to fire. Scanning them reports the engine's own test data as the repository's debt and blocks
every push that touches the corpus. The HISS scanner, the dedupe scan, gitleaks and the CI gofmt
sweep all skip the same directory name, which is Go's own convention for the same reason.

The semgrep stage has two forms and both apply the exclusion. A per-file scan drops fixture paths
from its file list. When a pushed range touches `.config/semgrep/`, the scan widens to the whole
tree and passes `--exclude testdata` to semgrep instead, because a bare `.` would walk into the
corpus. Both forms read the directory name from `FIXTURE_DIRECTORY`, so they cannot disagree.

## Running the gate on Windows

Four platform assumptions previously made `git commit` impossible on a Windows checkout. Each one
was only reachable after the previous was fixed, so they behaved as a single blocker.

- The binary is built as `praetorctl.exe` on Windows. `Makefile` derives the suffix from `$(OS)`,
  so every target names the binary for the host rather than for the developer's platform.
- The hook launches it by absolute path. `CreateProcessW` cannot resolve a bare relative path that
  uses forward slashes and lacks a `./` prefix, so `bin/praetorctl` failed before any check ran.
  The path resolves from the working directory, because the hook runs inside the repository being
  committed — during the harness self-tests that is a temporary fixture with its own `bin/`.
- Directory `fsync` is skipped on Windows. It is POSIX-only and NTFS refuses it on a directory
  handle, which failed the `commit-msg` live-state gate on every commit.
- The harness self-tests run everywhere, and skip only what they cannot build.

### A fresh worktree needs its ledger

`praetorctl state init --if-absent .` is required in every new worktree, on any platform, or the
gate fails with `working directory .workingdir does not exist`. `.workingdir/` is gitignored and
per-worktree, so it does not arrive with a checkout.

### Race-detector legs need cgo

`go test -race` requires cgo and a host C toolchain. Where `CGO_ENABLED` is `0` — a Windows box
without gcc, for instance — five self-test legs skip and **say why**:

```text
skipped 'race detector unavailable (CGO_ENABLED=0); CI runs these legs on Linux with cgo'
```

A skipped leg that read as a pass would certify what it never ran. CI runs on Linux with cgo, so
the coverage is deferred rather than lost.

This mattered beyond convenience. The self-tests trigger on any change under `.config/lefthook/`,
which is exactly where a contributor fixing Windows support has to work — so the gate could not be
repaired from the platform it was broken on.

### The hook scripts themselves run on Windows

Four POSIX-only calls in the scripts Lefthook invokes still failed there, each of them after the
CLI had already started:

- **The checkpoint policy read.** `checkpoint.py` opened the policy with `O_DIRECTORY`,
  `O_NOFOLLOW` and `dir_fd`, none of which exist on Windows, so the evaluator every adopted
  repository runs raised `AttributeError` before it read anything. The descriptor path is kept
  wherever the platform supports it. Elsewhere a checked reader inspects each component without
  following it, refuses links — junctions and other reparse points included — and non-directories
  with the same errors, and requires the file it opened to be the file it inspected. It *detects*
  a component replaced between the two, rather than preventing it, and says so.
- **Bounded subprocess output.** `common.py` waited on pipes with `selectors`; `select()` on
  Windows accepts only sockets, so every bounded `git` call raised `OSError`. Windows drains each
  pipe on its own thread under the same shared byte limit and deadline.
- **Stopping a bounded command that forked.** `common.py` stops the child's whole process group
  when a bound is exceeded, and Windows has no process group to signal. Killing the direct child
  alone left its children running past the bound, which is what `test_hooks.py` observed when a
  grandchild wrote its marker after the parent had been terminated. Windows asks
  `taskkill /F /T` to walk the descendants instead, and the direct kill remains the floor where
  that cannot run. Both bounded runners share the one function, so the two do not diverge.
- **The sandbox's user mapping.** `sandbox.py` passed `--user $(id -u):$(id -g)` so a bind mount
  keeps host ownership. `os.getuid` does not exist on Windows, where Docker Desktop maps that
  ownership itself, so the flag is omitted there.
- **The command-policy marker.** `block_evasion.py` printed `PRAETOR_COMMAND_POLICY_OK` through
  text mode, which Windows writes with CRLF. It is written as exact bytes.

One `prepare-commit-msg` defect surfaced with them and was never Windows-specific: the
instructional comment was added only when the message *source* was empty. Git omits that argument
entirely for a plain editor commit, and Lefthook then renders `{2}` as the literal string `2`, so
the commit that most needs the comment never received it on any platform. Only the sources git
documents — `message`, `template`, `merge`, `squash`, `commit` — now count as a source.
