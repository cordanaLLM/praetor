# Local Git hooks

Install Lefthook 1.13.6 or newer, then run `make hooks` and `make hooks-check`.
The configuration is tested with 1.13.6. Python 3, Git and the repository Go
version are required. Install `yamllint`, `shellcheck`, `actionlint` and `hadolint`
when editing their file types; applicable checks fail if their tool is missing.
Source pushes also require `gosec`, `govulncheck` and `semgrep`. Golangci-lint runs
from source using the existing repository `@latest` policy.

| Git stage | Work performed |
| --- | --- |
| `pre-commit`, `pre-merge-commit` | Check the exact index for whitespace, conflict markers, Python/JSON syntax, YAML, shell, workflow and Docker lint; Go formatting and vet on changed packages; verify affected generated agent instructions. |
| `prepare-commit-msg` | Add an instructional comment to a fresh empty message. |
| `commit-msg` | Require a conventional commit subject and DCO sign-off; accept Git merge/revert subjects. |
| `pre-push` | Inspect each actual pushed commit, run affected Go race tests, lint, security and vulnerability checks, plus applicable governance, flavor and ledger audits. |
| `post-commit` | Read the dedupe cadence and print a reminder when due; never run a scan or write the ledger. |
| `post-checkout`, `post-merge`, `post-rewrite` | Warm changed module dependencies in an isolated clone, rebuild the local CLI for source changes, verify affected agent outputs, report governance changes. File-only checkouts do nothing. |
| `pre-rebase` | Check the hook environment before replay begins. |

Pre-commit exports the index into a temporary directory. Unstaged edits, untracked
files and the index remain unchanged. Formatting is a read-only gate: run `gofmt`
and stage your chosen hunks explicitly. Deleted files are included when computing
scope and skipped by per-file linters. Paths are read with NUL delimiters and
passed as process arguments, including filenames containing spaces or shell text.
Missing tools and failed subprocesses stop blocking hooks.

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
`praetorctl state sync .` explicitly at turn end and stage intended ledger changes
as a separate action. Post hooks cannot undo an operation that already succeeded;
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
