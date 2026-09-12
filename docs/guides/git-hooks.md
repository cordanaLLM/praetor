# Local Git hooks

Install Lefthook 2.1.12 or newer, then run `make hooks` and `make hooks-check`.
The configuration is tested with 2.1.12. Python 3, Git and the repository Go
version are required. Install `yamllint`, `shellcheck`, `actionlint` and `hadolint`
when editing their file types; applicable checks fail if their tool is missing.
Strict source pushes also require `gosec`, `govulncheck` and `semgrep`. Golangci-lint runs
from source using the existing repository `@latest` policy.

For an isolated Semgrep installation, the push checks have been exercised with
1.177.0 on Python 3.14. Install it when `semgrep` is absent from `PATH`:

```bash
PRAETOR_TOOL_DIR="$HOME/.local/share/praetor-tools/semgrep-1.177.0"
python3 -m venv "$PRAETOR_TOOL_DIR"
"$PRAETOR_TOOL_DIR/bin/python" -m pip install 'semgrep==1.177.0'
mkdir -p "$HOME/.local/bin"
ln -s "$PRAETOR_TOOL_DIR/bin/semgrep" "$HOME/.local/bin/semgrep"
semgrep --version
```

Keep `$HOME/.local/bin` on `PATH`. The symlink command deliberately refuses to
replace an existing executable. See the [Semgrep package installation guidance](https://pypi.org/project/semgrep/1.177.0/).

| Git stage | Work performed |
| --- | --- |
| `pre-commit`, `pre-merge-commit` | Check the exact index for whitespace, conflict markers, Python/JSON syntax, YAML, shell, workflow and Docker lint; Go formatting and vet on changed packages; verify affected generated agent instructions. |
| `prepare-commit-msg` | Add an instructional comment to a fresh empty message. |
| `commit-msg` | Require a conventional commit subject and DCO sign-off; accept Git merge/revert subjects. |
| `pre-push` | Inspect each actual pushed commit. `checkpoint/*` destinations run file checks, affected Go builds and race tests. Other destinations also require lint, security, vulnerability, governance, flavor, ledger and signed-receipt gates. |
| `post-commit` | Read the dedupe cadence and print a reminder when due; never run a scan or write the ledger. |
| `post-checkout`, `post-merge`, `post-rewrite` | Warm changed module dependencies in an isolated clone, rebuild the local CLI for source changes, verify affected agent outputs, report governance changes. File-only checkouts do nothing. |
| `pre-rebase` | Check the hook environment before replay begins. |

Pre-commit exports the index into a temporary directory. Unstaged edits, untracked
files and the index remain unchanged. Formatting is a read-only gate: run `gofmt`
and stage your chosen hunks explicitly. Deleted files are included when computing
scope and skipped by per-file linters. Paths are read with NUL delimiters and
passed as process arguments, including filenames containing spaces or shell text.
A missing required tool or a failed subprocess blocks the operation.

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

Pre-push consumes Git's ref protocol once and checks disposable clones of those
commit IDs. It handles multiple refs, new branches, tag targets, deletions and
pushes that do not use the current checkout. An existing branch uses the remote
OID supplied by Git; a new branch uses a known remote ancestor, falling back to a
full-tree check when none is available. An unavailable remote object selects a conservative full-tree check. Empty input and ref deletions
have no new content to validate.

Go scope includes changed packages, test-only reverse dependencies, testdata and
embedded inputs of any extension. Module/build/security configuration selects all
packages. Deleted files beneath a package with embed patterns conservatively
select that package and its dependents. Go's package metadata is inspected before
skipping documentation, so embedded Markdown is still tested. Ordinary docs and
state edits skip Go race/security gates. Governance and flavor audits run for
source and policy/configuration edits; ledger edits run `state audit`. Source and
governance pushes also retain the full `gate run` pipeline and pinned-key
`gate verify`. Its full race/security stages replace duplicate scoped runs;
golangci-lint still uses affected packages. Verified receipts are retained under
Git metadata at `praetor-receipts/<commit>.json`, without staging an artifact. Existing
P0 blockers, audit failures, zero-warning lint failures and all gosec rules remain
enforced. The full gate and CI remain the final integration checks.

Use `git commit -s -m 'fix(scope): describe the change'` after reviewing your work.
The hook does not create a DCO attestation on your behalf. Run the required
`praetorctl state sync .` explicitly at turn end; keep the ledger local and untracked.
`make state-audit` initializes a missing ledger in a fresh checkout, then audits it;
existing incomplete, malformed, or P0-blocked state still fails. Post hooks cannot
undo an operation that already succeeded;
resolve any reported refresh failure before continuing.

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

## Codex command guard

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

No repository `Stop` verification hook is configured. `make verify-all` remains
an explicit required agent step, while commits and pushes have the Git gates
listed above. See the [Codex hook lifecycle and trust documentation](https://learn.chatgpt.com/docs/hooks)
for runtime coverage and activation semantics.


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
