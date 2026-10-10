# Local Git hooks

Install Lefthook 2.1.14 or newer, then run `make hooks` and `make hooks-check`.
The configuration is tested with 2.1.14. Python 3.10 or newer, GNU Make, Git, a POSIX `sh`
and the repository Go version are required. The hooks find the interpreter and `make`
themselves, from fixed names, and prove each before they run it
([the interpreter the hooks run](#the-interpreter-the-hooks-run),
[the make the hooks run](#the-make-the-hooks-run)).
Install `yamllint`, `shellcheck`, `actionlint` and `hadolint`
when editing their file types; applicable checks fail if their tool is missing
or older than its floor ([linter version floors](#linter-version-floors)).
Strict source pushes also require `gosec`, `govulncheck` and `semgrep`. Golangci-lint runs
from source using the existing repository `@latest` policy. `tools/go/go.mod` pins `gosec`,
`govulncheck` and `gitleaks` as tool directives, which Renovate keeps current: `make sec`,
`make vuln` and `make secrets` run exactly those versions through `go tool -modfile` (`make vuln`
hands govulncheck to `praetorctl security govuln`, the
[Go vulnerability gate](adoption-verification.md#go-vulnerabilities-and-the-openvex-document)), and
`go install -modfile=tools/go/go.mod golang.org/x/vuln/cmd/govulncheck github.com/securego/gosec/v2/cmd/gosec`
puts the same versions on `PATH` for the hooks.

`make verify-all` also runs `make semgrep-test`, which checks the real matcher on
temporary positive, negative and boundary fixtures. The engine version comes from
`.config/semgrep/requirements.txt`; missing or mismatched engines fail the check.
This is rule regression coverage, not a replacement for repository source scans.
For an isolated installation, use the tested 1.178.0 release (Python 3.14 locally):

```bash
PRAETOR_TOOL_DIR="$HOME/.local/share/praetor-tools/semgrep-1.178.0"
python3 -m venv "$PRAETOR_TOOL_DIR"
"$PRAETOR_TOOL_DIR/bin/python" -m pip install -r .config/semgrep/requirements.txt
mkdir -p "$HOME/.local/bin"
ln -s "$PRAETOR_TOOL_DIR/bin/semgrep" "$HOME/.local/bin/semgrep"
semgrep --version
```

Keep `$HOME/.local/bin` on `PATH`. The symlink command deliberately refuses to
replace an existing executable. See the [Semgrep package installation guidance](https://pypi.org/project/semgrep/1.178.0/).

`make verify-all` also runs `make hooks-lint` (`scripts/test_emitted_hook_lint.py`). It lints
the Praetor-owned canonical hook sources: `.config/lefthook/scripts/checkpoint.py` and
`common.py`, which adoption copies into a repository, and `.config/lefthook/praetor.yml`, the
vendorable canonical policy. Adoption does not write `praetor.yml`; an adopter vendors it by
hand with its scripts and extends it from their own `lefthook.yml`, as
`.config/lefthook/README.md` describes. These files must pass the linters an adopter's own
hooks may run: black and yamllint with their built-in defaults (yamllint in strict mode) and
flake8 at 100 columns. The gate lints copies in an empty directory with configuration files
ignored, and checks negative and boundary fixtures so the policy is proven to be on.
The same run covers every managed asset adoption writes, enumerated from the registry by
`go run ./internal/managedasset/export` and not by a list kept in the script: the documentation
gate's workflow and YAML, the figure engine's Python and the API compatibility gate's Go program.
Audit holds an adopter's copies byte-equal, so an adopter cannot fix a finding in them. They are
held to black, flake8, yamllint, `ruff check` and `ruff format --check` (Python), gofmt, gofumpt
and `go vet` (Go) and shellcheck (shell); the policy is in the script's docstring and in the
[figures guide](figures.md#locked-assets-and-linters).

The gate also lints the two hook files adoption renders from templates in
`internal/adopt/hooks.go`: the root `lefthook.yml` (`buildLefthookYAMLFor`) and
`.config/agent/hooks/block_evasion.py` (`blockEvasionTemplate`). Their renderings are
committed under `internal/adopt/testdata/emitted`, at the paths adoption writes them, and
`TestEmittedHookFixturesMatchTheRendering` fails when a fixture differs from the rendering.
After a template change, regenerate them with
`PRAETOR_UPDATE_EMITTED_FIXTURES=1 go test ./internal/adopt -run TestEmittedHookFixturesMatchTheRendering`.
The rendered `lefthook.yml` opens with a document start and folds every `run` line longer than
80 columns into a `>-` block, which YAML reads back as the same one-line command
(`TestLefthookRendering_Positive_FoldsWithoutChangingValues`). The rendered interceptor splits
long rule patterns and refusal texts into adjacent string literals laid out as black leaves
them (`internal/adopt/emitted_layout.go`), and every function stays within the 35 lines of
the strictest public dogfood policy (`TestVendoredCheckpointSourcesFitStrictAdopterLOC`).
A `lefthook.yml` an earlier release rendered with unfolded lines is still recognised and
migrated on the next adoption (`priorLefthookDigests` in
`internal/adopt/lefthook_identity.go`).

The tools come from the hash-locked `.config/hook-lint/requirements.txt`, which Renovate
recompiles from `requirements.in`:

```bash
python3 -m venv "$HOME/.local/share/praetor-tools/hook-lint"
"$HOME/.local/share/praetor-tools/hook-lint/bin/python" -m pip install --require-hashes -r .config/hook-lint/requirements.txt
PRAETOR_HOOK_LINT_BIN="$HOME/.local/share/praetor-tools/hook-lint/bin" make hooks-lint
```

gofumpt is pinned in `tools/go/go.mod` instead, the one version source for Go tools; install it
into the same directory with
`GOBIN="$HOME/.local/share/praetor-tools/hook-lint/bin" go install -modfile=tools/go/go.mod mvdan.cc/gofumpt`.
`go`, `gofmt` and `shellcheck` come from `PATH`.

Without `PRAETOR_HOOK_LINT_BIN` the gate takes the tools from `PATH`, and each check skips
with its reason when its tool is missing or is not the pinned version. With the variable set,
as CI sets it, a missing or mismatched tool fails the gate.

`make hooks-lint` also runs `scripts/test_emitted_yaml_lint.py`, which reuses that gate's tool
resolution to run `yamllint --strict -d default` over the other YAML Praetor writes into a
repository:

- `.config/labels.yaml`, linted as praetor's own copy, which `internal/forge/labels_test.go`
  holds byte-equal to `forge.DefaultLabelTaxonomy`;
- every profile and facet under `.config/archetypes`, which adoption copies byte for byte into
  the adopter's pinned catalog;
- the `.standards.yaml` rendering (`config.RenderManifest`, which adoption, `praetorctl init` and
  onboarding share), from `internal/adopt/testdata/emitted/.standards.yaml`, which
  `TestEmittedHookFixturesMatchTheRendering` keeps equal to the rendering of the declarations in
  `internal/adopt/testdata/manifest/prior.standards.yaml`;
- every `.yml` and `.yaml` body under `templates/` that flavor apply scaffolds, and every
  template `internal/flavor/definitions.go` checks with a YAML validator whatever its name
  (`.clang-format` and `.clang-tidy`, which the Visual Studio editor target also writes), with
  its leading template comment dropped as rendering drops it
  (`test_every_yaml_validated_flavor_template_is_linted`). A body whose actions branch on the
  repository (the Node CI job per package manager, the Dart analyzer config per lint package) is
  linted from its committed renderings under `templates/testdata/rendered/`, one per distinct
  body. `TestBranchingYAMLTemplateRenderingsAreCommitted` (`templates/branching_test.go`) renders
  every combination of the facts the template reads and keeps those files equal to the result.

Each path comes from the Go constant that names it. The gate also appends an overlong line to
every file and requires yamllint to reject each one, so the rule is proven to be on.

| Git stage | Work performed |
| --- | --- |
| `pre-commit`, `pre-merge-commit` | Audit the live private ledger, then check the exact index for whitespace, conflict markers, Python/JSON syntax, YAML, shell, workflow and Docker lint; Go formatting and vet on changed packages; verify affected generated agent instructions. |
| `prepare-commit-msg` | Add an instructional comment to a fresh empty message. |
| `commit-msg` | Verify the live state synchronization after Lefthook restores partially staged worktree files, then apply the commit message policy (`praetorctl forge check-message`: a conventional subject, Git merge/revert subjects accepted, and the HISS-14 `Migration:` footer on a breaking change) and require a DCO sign-off. |
| `pre-push` | Audit and verify live state before inspecting each actual pushed commit. `checkpoint/*` destinations run file checks, affected Go builds and race tests. Other destinations also require lint, security, vulnerability, governance, flavor and signed-receipt gates. |
| `post-commit` | Synchronize and read back the private state for the new commit, then print the dedupe cadence reminder when due. Sync failures are reported. |
| `post-checkout`, `post-merge`, `post-rewrite` | Warm changed module dependencies in an isolated clone, rebuild the local CLI for source changes, verify affected agent outputs, report governance changes. `post-merge` then runs `praetorctl workstation install --if-stale`, which rebuilds a lagging engine install from this checkout on the update branch and otherwise skips silently ([refresh a lagging install](workstation-update.md#refresh-a-lagging-install)). File-only checkouts do nothing. |
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
`templates/` recursively, so `<chart>/templates/rbac/role.yaml` is as much a template as
`<chart>/templates/service.yaml`. Those files are Go templates that render YAML, so a YAML parser
rejects `{{- if }}` before a single rule can run. The chart's own `Chart.yaml` and
`values.yaml` are ordinary documents and stay linted, and a `templates/` directory with no
`Chart.yaml` beside it is not a chart at all.

A staged change to `lefthook.yml`, a native client hook file (`.claude/settings.json`,
`.codex/hooks.json`, `.gemini/settings.json`), the AGY plugin's `hooks.json` or guard copy
under `.agents/plugins/praetor/`, anything under `.config/lefthook/` or `.config/agent/`, or
one of the two hook test scripts runs the harness self-tests
(`.config/lefthook/scripts/checks.py`): `lefthook validate`, `test_hooks.py`,
`test_security_scope.py`, `test_checkpoint.py`, `scripts/test_checkpoint_hooks.py`, and
`scripts/test_praetor_hook.py`. The last one runs the tracked agent-hook strings through
`sh -c` against the skew guard `.config/agent/hooks/praetor_hook.py` and its AGY plugin copy
([Agent hooks](agent-hooks.md#rollout-engine-skew-never-blocks-a-client)). `make hooks-test`
runs the same scripts.

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

## Linter version floors

`shellcheck`, `actionlint`, `hadolint` and `yamllint` run by name from `PATH`, so the hook
checks each one before it trusts its verdict. `.config/lefthook/tool-floors.txt` declares
them, one line per linter in the shape of the `requirements` files beside it:

| Line | The hook requires |
| :--- | :--- |
| `tool>=version` | the linter, at that version or newer |
| `tool` | the linter, at any version |

A floor needs a source in this repository, and each line's comment names it: the
`actionlint` release `.github/actionlint.yaml` measures against, the `shellcheck` release
`.github/workflows/portability.yml` installs and the `yamllint` pin in
`.config/hook-lint/requirements.in`. `hadolint` has no floor. No workflow, container image
or configuration file here installs it or names a version of it, and a version taken from
one workstation would refuse contributors on another without evidence that the verdict
on the root `Dockerfile` differs.

Before any linter runs, `file_checks` (`.config/lefthook/scripts/checks.py`) passes the
linters with files to check to `require_floors` (`.config/lefthook/scripts/toolchain.py`),
which reads each floored one's `--version` in one process bounded to 30 seconds. The commit
or push is refused, with the requirement and the floors file named, when a linter:

- is not installed or does not start;
- prints no version the policy reads, such as an `actionlint` built from an untagged
  checkout, which prints `(devel)`;
- is older than its floor.

A newer release passes: a floor is the oldest version the tree's verdict is known to hold
with, not a pin. A linter is probed only when a file of its type is staged or pushed, so a
missing `hadolint` blocks a commit that stages a `Dockerfile` and no other. A linter the hook
runs without a line in the floors file fails the same way, as does a floors file that is
unreadable or holds a line in any other shape.

```text
praetor hooks: shellcheck >= 0.11.0 is required (.config/lefthook/tool-floors.txt): the shellcheck on PATH is 0.9.0
praetor hooks: hadolint is required (.config/lefthook/tool-floors.txt, any version): it is not on PATH
```

The `ToolFloors` cases in `.config/lefthook/scripts/test_hooks.py` cover each outcome and
replay the version readers against the linters installed on the host. The
`HookToolFloorSources` cases in `scripts/test_portability_selftest.py` hold each floor to its
source: they fail when the `yamllint` pin or the `shellcheck` release falls below its floor,
when `.github/actionlint.yaml` measures against another `actionlint` release than the floor,
and when a workflow or the development container starts to name `hadolint` while its line
still declares no floor. To raise a floor, change its line together with the source its
comment names.

One reader serves every requirement line (`requirement` in `toolchain.py`): the floors file
and the hash-locked lint lock that `scripts/test_emitted_hook_lint.py` installs from are
parsed by it, and `stated_version` is the one version probe behind the linters, the
interpreter and `make`.

## The interpreter the hooks run

No hook command names a Python interpreter, and no environment variable selects one. Every
job in `.config/lefthook/praetor.yml` and the pre-push script start their hook through
`.config/lefthook/python.sh`, which tries a fixed list of candidates in order and runs the
first that proves itself:

| Candidate | Where it is the interpreter |
| :--- | :--- |
| `python3` | the name Linux and macOS give Python 3 |
| `python` | the command an install on Windows provides |
| `py -3` | the `py` launcher an install on Windows provides, asked for Python 3 |

The Windows commands are the ones
[Using Python on Windows](https://docs.python.org/3/using/windows.html) lists.

A candidate proves itself by running a one-line program that prints its version, and the
launcher accepts it only when the answer is Python 3 at 3.10 or newer, the release
`.config/hook-lint/requirements.txt` is compiled for. Finding a file of that name is not
proof. Windows 10 and 11 put `python3.exe` and `python.exe` on the user's `PATH` as App
Execution Aliases for the Microsoft Store: every `PATH` lookup finds them, and started with
arguments they print `Python was not found` and exit nonzero. Such a candidate, a program
that exits 0 without answering and a Python older than the floor are skipped for the next
one. The proven candidate then replaces the launcher's shell, so the status it returns is
the hook's verdict and nothing else's.

With no candidate left the hook fails with status 127 and names what it tried:

```text
praetor hooks: missing dependency: no Python 3.10 or newer interpreter.
Tried: python3, python, py -3; none answered the version probe.
Install Python 3.10 or newer under one of these names.
```

A variable that named the program a hook starts would turn every gate into a pass once it
named a program that exits 0, so none is read. `test_no_variable_selects_the_interpreter`
and `test_no_variable_takes_a_hook_job_off_its_interpreter_or_its_make` set such variables
and require the hook to judge as before.

A hook that starts another Python process, such as the harness self-tests or the command
guard, reuses the interpreter it is running under (`sys.executable`), so a second
interpreter can never run beside the first.

The candidate list exists once per language that needs it and tests hold the copies equal:

| Where | Used by | Held equal by |
| :--- | :--- | :--- |
| `.config/lefthook/python.sh` | every Lefthook job, before any interpreter runs | the two tests below |
| `PYTHON_CANDIDATES`, `PYTHON_FLOOR` and `PYTHON_PROBE` in `.config/lefthook/scripts/toolchain.py` | the report below and `scripts/dev_install.py` | `test_the_launcher_and_the_policy_declare_one_candidate_list`, and `test_candidates_are_tried_in_order_and_held_to_the_floor`, which runs both on the same stand-in programs |
| the `hooks.python` default in `internal/config/operator_sections.go` | the Go checkpoint evaluator ([checkpoint cadence](checkpoint-cadence.md)) | `TestHookLauncherCandidatesAreTheOperatorDefault` in `internal/forge/hook_toolchain_guard_test.go` |

`scripts/dev_install.py` resolves the same list before it installs anything
(`hook_interpreter`, which calls `python_program` in `toolchain.py`). It reports the command
and version the hooks will start under `hook_interpreter` in its install report, stops when
no candidate proves itself, and stores nothing in the environment.

Every line of the launcher ends in a comment sign. `praetorctl adopt` copies the file into
other repositories ([the launcher adoption writes](#the-launcher-adoption-writes)), where a
checkout under `core.autocrlf` may give it CRLF line endings, and a shell reads a carriage
return as part of the last word of a line; behind a comment sign it is part of the comment
(`test_a_crlf_checkout_of_the_launcher_runs_the_hook`).

To see what a host provides, run the declared-toolchain report. It resolves the interpreter
and `make` from their candidates, looks up each program the hooks start by name and reads
each floored linter's version:

```bash
python3 -B .config/lefthook/scripts/toolchain.py
python3 -B .config/lefthook/scripts/toolchain.py --require python,make,sh,shellcheck,yamllint
```

Each declared tool gets one line. Without `--require` the run always exits 0 and a tool that
is missing is reported as `not asserted` with the message a hook would give; with it, a named
tool that is unusable is reported as `UNUSABLE` and the run exits 1. The Platform Neutrality
job runs the second form on every leg before the harness self-tests
([HISS-21](../standards/hiss-21-platform-neutrality.md#enforcement-in-this-repository)).

The `HookInterpreter` cases in `.config/lefthook/scripts/test_hooks.py` cover the launcher,
and `test_a_hook_job_refuses_stand_ins_for_its_interpreter_and_its_make` drives a real
Lefthook job with stand-in programs first on `PATH`. The agent-client registrations
(`.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json`) still name `python3`
themselves; see [agent hooks](agent-hooks.md).

### The launcher adoption writes

The `lefthook.yml` that `praetorctl adopt` writes starts its two checkpoint jobs through the
same launcher. Adoption copies `.config/lefthook/python.sh` from the `--lock-source-root`
bundle with the checkpoint scripts (`checkpointBundle` in `internal/adopt/checkpoint.go`), and
each job runs `sh .config/lefthook/python.sh -B .config/lefthook/scripts/checkpoint.py ...`
(`lefthookPythonCommand` in `internal/adopt/hooks.go`).

The generated line holds a script path and plain arguments, with no quote and no expansion.
Lefthook's Windows executor hands a run line to `sh -c` inside one pair of double quotes
without escaping the quotes in it, so a rule stated inline with a quoted variable would lose
its quotes there and split an interpreter path that holds a space.
`TestLefthookPythonCommandStartsTheBundledLauncher` refuses any such character in the line,
and `TestLefthookPythonCommandRunsTheHookThroughTheLauncher` runs it through `sh`, also with
a CRLF copy of the launcher.

A `lefthook.yml` from an earlier release, whose checkpoint jobs named `python3`, is migrated
on the next adoption from a source bundle, and the launcher is installed in the same run
(`TestAdopt_Boundary_Python3ByNameRenderingMigratesToTheLauncher`). A source root without the
launcher installs no part of the bundle and enables no checkpoint job
(`TestCheckpointLauncher_Negative_SourceWithoutItInstallsNothing`).

An unedited `checkpoint.py` from an earlier release is refreshed on the next adoption, because
every text a release shipped is recorded with its digest (`priorCheckpointDigests` in
`internal/adopt/checkpoint.go`, reproduced by `internal/adopt/testdata/checkpoint`). A change
to the script records its new digest in the same change. The current script judges a re-run of
a hosted check by its latest run on the head, and on a draft reads a hosted gate's failure that
carries exactly the draft annotation as `draft_pending` rather than `failed`;
[checkpoint cadence](checkpoint-cadence.md) states both rules.

## The engine the hooks run

Every governance job of the generated `lefthook.yml` and the fallback `pre-commit` hook run
`sh .config/lefthook/engine.sh <arguments>` instead of a `praetorctl` found on `PATH`
(`lefthookGovernedCommand` in `internal/adopt/hooks.go`; the script is
`engineLauncherScript` in `internal/adopt/engine_launcher_script.go`, installed by `praetorctl adopt`).
A newer binary on `PATH` therefore never judges a repository that pins an older engine: the hook runs the engine
the repository declares, the one its hosted Standards job installs.

**The pin** is the value of `PRAETOR_REF` declared in any file under `.github/workflows`, as
`PRAETOR_REF: <ref>` or `PRAETOR_REF=<ref>`, or the `uses:` reference of `praetor-adopt`
(`uses: cordanaLLM/praetor/.github/actions/praetor-adopt@<ref>`) or a reusable workflow under
`cordanaLLM/praetor/.github/workflows/` (quotes, a trailing comment and CRLF line endings
are read past). A ref is a commit id of 7 to 40 hexadecimal digits or a full release tag such as
`v1.2.3` (a tag without patch digits like `v1` or `v1.2` moves under `go install`, and a branch
name moves, so they are no pin). A non-pin `uses:` ref such as `@main` is treated as unpinned (running
the engine on `PATH`). Declaring two different pin values fails closed with refusal.

**The cache** is `${XDG_CACHE_HOME:-$HOME/.cache}/praetor/engine/<pin>`, one directory per pin.
The first hook run with a pin that is not cached installs it:

```sh
GOBIN=<cache>/<pin> go install github.com/cordanaLLM/praetor/cmd/standardsctl@<pin>
```

The install goes through a scratch directory whose files are renamed into place, so an interrupted run leaves no
half-written engine and parallel jobs on a cold cache cannot remove each other's engine
(`TestEngineLauncher_Boundary_ConcurrentColdInstalls`), and it is bounded by `PRAETOR_ENGINE_INSTALL_TIMEOUT` seconds (default
300, must be an integer >= 1). Later runs find the cached binary and install nothing. Changing the pin selects, and
installs when needed, another directory; old directories stay until you delete them.

**Without a pin** the hook runs the binary on `PATH`, `praetorctl` before `standardsctl`, as
before, and prints `praetor hooks: no PRAETOR_REF pin under .github/workflows; running the
engine on PATH: <path>` on standard error.

**It never falls back.** A pin that is unusable, declared twice with different values, an unreadable workflow file, or an install that
cannot complete (no Go toolchain, no network, a timeout) fails the hook with one message
that names the pin, the cache directory and the command that fixes it, for example:

```text
praetor hooks: installing the engine pinned by PRAETOR_REF=492a00f930e1 (declared in .github/workflows/standards-gate.yml), engine cache /home/me/.cache/praetor/engine/492a00f930e1 failed (exit 1, timeout 300 seconds); run: GOBIN=/home/me/.cache/praetor/engine/492a00f930e1 go install github.com/cordanaLLM/praetor/cmd/standardsctl@492a00f930e1
```

The launcher is a POSIX shell script that runs under Git Bash on Windows, and every line ends
in a comment sign for the reason given for the [Python launcher](#the-launcher-adoption-writes).

Generated `Makefile`s and adopted `Makefile`s include `.config/praetor/engine.mk` when the hook launcher is installed or planned,
a Praetor-managed file that resolves `$(PRAETORCTL)` through `.config/lefthook/engine.sh`
and provides the `praetor-engine-path` target, aligning Make targets and hooks with the repository's pinned
engine (#906, HISS-19). To avoid becoming Make's default goal when included before the first target,
`engine.mk` guards `praetor-engine-path` by saving `$(.DEFAULT_GOAL)` before the rule and restoring it afterward.
For an adopter-owned `Makefile`, adoption inserts
`-include .config/praetor/engine.mk` near the top (after leading comments), leaving existing rules in place.
Because the first `?=` assignment wins in Make, any later fallback assignment becomes a
no-op without needing to recognize or edit existing verification blocks. An adopter's assignment
using `:=`, `::=`, `=`, `!=`, `+=`, or `define` is respected and reported as a warning naming the line. An assignment using `?=` is shadowed by `.config/praetor/engine.mk` and is reported as a warning advising `:=` or `=` to override.

**One pin reader.** `.config/lefthook/engine.sh` is also a standalone entry point: an adopter
`Makefile` target or script runs `sh .config/lefthook/engine.sh <arguments>` from the repository
root to get the same pin resolution, cache and refusals as the hooks, and needs no second reader
of `PRAETOR_REF`. When a recipe or rule needs the binary path directly, `--print-path` prints
only the path of the resolved binary, which `.config/praetor/engine.mk` uses:

```makefile
-include .config/praetor/engine.mk
```

Tests (`internal/adopt/engine_launcher_test.go`): `TestEngineLauncher_Positive_PinnedEngineWinsOverPath`
runs stub binaries that print their identity with a different `praetorctl` first on `PATH`;
`TestEngineLauncher_Positive_InstallsOncePerPin`, `TestEngineLauncher_Negative_UninstallablePinFailsClosed`,
`TestEngineLauncher_Negative_NoGoToolchainFailsClosed`, `TestEngineLauncher_Boundary_InstallTimeout`,
`TestEngineLauncher_Negative_UnusablePinsFailClosed`, `TestEngineLauncher_Negative_ConflictingPinsFailClosed`,
`TestEngineLauncher_Positive_UsesRefPinsEngine`, `TestEngineLauncher_Boundary_NonPinUsesRefRunsOnPath`,
`TestEngineLauncher_Negative_UnreadableWorkflowFailsClosed`, `TestEngineLauncher_Positive_PrintPath`,
`TestEngineLauncher_Negative_PrintPathFailsClosedOnUnusablePin`, `TestEngineLauncher_Boundary_InstallTimeoutZeroFailsClosed`
and `TestEngineLauncher_Boundary_NoSleepSurvivesInstall` cover the cache, the pin sources, `--print-path` and the refusals;
`TestEngineLauncher_Boundary_NoPinKeepsPathBehaviourAndSaysSo` the unpinned case.

## The make the hooks run

The hooks need GNU Make on every platform. `pre-commit`, `commit-msg`, `pre-push` and the
`post-*` hooks build the repository's CLI through the `hook-cli` Make target before they run
it (`cli`, `refresh` and `refresh_install` in `.config/lefthook/scripts/hooks.py`), and a
strict push runs the `state-audit` target (`source_checks` in `checks.py`). They pass
`--always-make` and `--no-print-directory`, which are GNU Make options.

All four calls take the program from `make_program` (`.config/lefthook/scripts/toolchain.py`).
It tries `make`, then `gmake`, then `mingw32-make`, skipping a name that is not on `PATH`,
and uses the first whose `--version` output begins with `GNU Make`. `gmake` is the name GNU
Make has where `make` is another make, as on the BSDs; `mingw32-make` is its name on a MinGW
host. A candidate that exits 0 without stating GNU Make is skipped like one that fails, and
no environment variable selects the program, for the reason given for the interpreter.

Before the CLI is built, `pre-commit` has already run the environment guard, read the staged
paths and checked the index for private state. When no candidate proves itself, the hook
stops at that point, before the CLI is built and before any check that needs it:

```text
praetor hooks: missing dependency: GNU Make, which builds the CLI the hooks run (the hook-cli target). Tried: make (not on PATH); gmake (not on PATH); mingw32-make (not on PATH)
```

Nothing is skipped in that case. A hook that cannot build the CLI would run a stale binary
or none, so a missing `make` refuses the commit or push and says what was tried.

The declared-toolchain report above prints the GNU Make version and the path the hooks would
run. The Platform Neutrality job requires it on every leg before the self-tests; no leg
installs it, each uses the one its image carries
([HISS-21](../standards/hiss-21-platform-neutrality.md#the-hook-toolchain-is-declared-and-the-matrix-asserts-it)).
The `HookMake` cases in `.config/lefthook/scripts/test_hooks.py` cover the resolution, and
the Lefthook job test named above puts stand-ins for all three names first on `PATH`.

## The lefthook.yml adoption writes

`praetorctl adopt` writes a root `lefthook.yml` (`buildLefthookYAMLFor` in
`internal/adopt/hooks.go`). Its governance jobs are the same in every repository:
`compile-context --verify` and `audit --offline` before a commit, `state sync` and the dedupe
cadence after one, and `flavor audit`, `audit` and `gate run` before a push (see
[Offline before a commit, online before a push](#offline-before-a-commit-online-before-a-push)).
The language jobs follow the languages the adoption verification plan detects from root markers,
the set the harness rows are rendered for (`lefthookLanguages`, `planLanguages` in
`internal/adopt/harness_hiss.go`):

| Detected | Pre-commit jobs | Pre-push jobs | Each runs only where |
| --- | --- | --- | --- |
| Go (`go.mod`) | `gofmt` (`gofmt -w` on staged files), `govet` (`go vet ./...`) | `security` (`praetorctl security govuln`, the [Go vulnerability gate](adoption-verification.md#go-vulnerabilities-and-the-openvex-document); skipped when `govulncheck` is not installed) | Go files are staged, and the root holds `go.mod` for `go vet` and the gate |
| Rust (`Cargo.toml`) | `rustfmt` (`cargo fmt --all --check`), `clippy` (the gate's own `cargo clippy --workspace --all-targets -- -D warnings`, `gating.CargoClippyArgs`) | none: `gate run` runs `cargo audit` for a `Cargo.lock` | Rust files are staged and the root holds `Cargo.toml` |
| none detected | every language's jobs above, as the harness then keeps every HISS clause | as above | as above |

Any other detected language, such as Python, gets the governance jobs alone. A Go module kept
only in a subdirectory is not detected, so a Cargo workspace with a Go tool under `tooling/`
gets the Rust jobs. The header comment names the languages the file carries jobs for. Tests:
`TestAdopt_Positive_LefthookJobsFollowDetectedLanguages` and
`TestAdopt_Boundary_LefthookJobsForUnknownAndJoblessLanguages` in
`internal/adopt/lefthook_languages_test.go`, and the `TestCargoJobs_*` and `TestGoModuleJobs_*`
cases in `internal/adopt/hooks_gomod_test.go`, which run the job lines against stub tools.

A root that carries `REUSE.toml` or a `LICENSES/` directory also gets a `reuse-lint` pre-commit
job (`reuseLintCommand`): `reuse lint` with the reuse major that `fsfe/reuse-action@v6` runs in
the hosted REUSE gate adoption writes beside it
([what adoption scaffolds](../adoption.md#what-adoption-scaffolds-automatically)). It skips,
saying why, where reuse is not installed or the root has lost both markers, and fails, naming the
pin, on another reuse major. A `lefthook.yml` adoption wrote before the root gained or lost the
markers is migrated without `--force`, keeping its checkpoint jobs.
`TestReuseLintCommand_RunsTheLintAtThePinnedMajor` in `internal/adopt/reuse_gate_test.go` runs
the line against a stub reuse, and `TestReuseLintCommand_Negative_UnlabelledFileFailsTheJob`
against the installed one.

The pre-push `gate` job runs `gate run --path=. --admit-unsupported` (`prePushGateArgs`). A root
with neither `go.mod` nor `Cargo.lock`, such as a Meson, CMake, npm or Python repository, gets no
receipt, because the gate runs no toolchain for it: the job runs every other stage, names the
languages it could not verify and admits the push instead of refusing every one. A root with either
marker still fails the job when its toolchain stages did not run
([The pre-push hook admits languages the gate has no runner for](adoption-verification.md#the-pre-push-hook-admits-languages-the-gate-has-no-runner-for)).
`TestLefthookGate_Positive_PrePushGateAdmitsUnsupportedLanguages` in
`internal/adopt/lefthook_gate_admit_test.go` runs the job line against a stub.

The `rust-systems` flavor describes this setting as pre-commit clippy and rustfmt enforcement,
and `flavor audit` holds it to that: `lefthook.yml` satisfies it only when its `pre-commit` run
lines call `cargo fmt` (or `rustfmt`) and `cargo clippy`, in the commands map or a jobs list
(`validRustLefthook` in `internal/flavor/lefthook_setting.go`, tests in
`internal/flavor/lefthook_setting_test.go`). The other flavors accept any non-empty mapping.

An existing `lefthook.yml` is classified before anything is installed (`classifyLefthookConfig` in
`internal/adopt/lefthook_identity.go`):

- **A current rendering** for the repository's languages is verified and activated. A CRLF
  checkout of one is verified and kept without `--force`, and rewritten with its LF bytes under
  `--force`, since activation trusts only those. The rendering without checkpoint jobs gains them
  once the checkpoint lifecycle is installed; the one with them is kept by a run that did not
  install the lifecycle (`internal/adopt/lefthook_keep_test.go`).
- **An earlier rendering** listed in `priorLefthookDigests`, reproduced by
  `internal/adopt/testdata/lefthook/`, is migrated to the current one on a plain run and
  activated. The list holds the two renderings that carried the Go jobs into every repository,
  so those adopters get the jobs of their own languages without `--force`
  (`TestAdopt_Negative_GoEveryRepositoryRenderingMigratesToCargoJobs`). It also holds the
  renderings whose pre-commit audit still read the forge, one per language set with and without
  checkpoint jobs, so those adopters get `audit --offline` before a commit on a plain run
  (`TestAdopt_Boundary_OnlinePreCommitAuditMigratesWithoutForce`), and the renderings whose `gate`
  job ran without `--admit-unsupported`, so a Meson repository's pushes stop being refused on a
  plain run (`TestAdopt_Boundary_StrictPrePushGateMigratesWithoutForce`), and the Go renderings
  whose `security` job ran `govulncheck ./...`, so a Go repository gets the vulnerability gate on a
  plain run (`TestAdopt_Boundary_PlainGovulncheckMigratesWithoutForce`).
- **Any other file** is the repository's. It is kept byte for byte, `--force` included, and not
  activated: the audit checks only that `lefthook.yml` exists, so `--force` has nothing to restore.
  The skip names the generated jobs the file lacks and the jobs it adds, or says the file does not
  parse as a YAML mapping. A file that extends `.config/lefthook/praetor.yml` is reported as the
  canonical policy instead. Beside a kept file adoption writes only the checkpoint files that
  are absent and refreshes none (`TestAdopt_Negative_ForeignLefthookKeptWithAndWithoutForce`,
  `TestAdopt_Positive_ForceKeepsAdopterExtendsLefthook`,
  `TestAdopt_Boundary_UnparsableLefthookKeptAbsentCreated`,
  `TestAdopt_Positive_KeptLefthookGetsOnlyAbsentCheckpointFiles`).

To regenerate a kept file, remove `lefthook.yml` and re-run adopt, or merge the named jobs by
hand and run `lefthook install`. After changing the template, record the rendering it replaced:
copy it under `internal/adopt/testdata/lefthook/` and add its digest to `priorLefthookDigests`
(`TestPriorLefthookDigests_Positive_ReproducedByFixtures`).

### Offline before a commit, online before a push

The two audit jobs differ on purpose (`preCommitAuditArgs` and `prePushAuditArgs` in
`internal/adopt/hooks.go`):

| Job | Command | Reads the forge |
| --- | --- | --- |
| `pre-commit` `hiss-audit`, and the fallback pre-commit hook | `praetorctl audit --offline` | never |
| `pre-push` `audit` | `praetorctl audit` | when the [live Actions checks](actions-live-checks.md) can run |

A commit reads files only, so it never waits on the network and never fails on a forge
setting. A push runs the live Actions checks when the `origin` remote names the manifest's
repository on github.com and a token is found. That costs up to 130 GitHub API requests: two
for the workflow permissions, and one or two for each of at most 64 workflows. Each request is
bounded at 15 seconds and all of them together at three minutes. Without `GITHUB_TOKEN` or
`GH_TOKEN` the audit also runs `gh auth token` once. A push whose live workflow permissions
have drifted from `overrides.actions` is refused until the setting the verdict names is fixed.
`make verify-all` and CI run the online audit too; whether it reaches the forge there depends
on the token they provide. Tests: `TestLefthookAudit_Positive_PreCommitOfflinePrePushOnline`
in `internal/adopt/lefthook_audit_offline_test.go`.

## The hook runner the audit accepts

Unless `.standards.yaml` lists `git-hooks` in `adoption.decline`, `praetorctl audit` requires
two things in a Git checkout: a hook runner's configuration, and a `pre-commit` hook that this
runner installed. Both checks live in `internal/adopt/hook_runner_audit.go`:

- `AuditGitHookConfig` checks the configuration. `praetorctl audit` and `standards_audit` both
  run it. It accepts `lefthook.yml`. Without `lefthook.yml`, it accepts a
  `.pre-commit-config.yaml` that runs both praetor commands (see below).
- `AuditInstalledGitHook` checks the installed hook. Only `praetorctl audit` runs it, and not in
  CI, which installs no hooks. In CI the audit prints the runner and the configuration file it
  verified instead.

Adoption writes `lefthook.yml` and never a pre-commit framework configuration. The hook is the one
in the managed hooks directory, `<git-common-dir>/hooks`, which every linked worktree shares.

`core.hooksPath` moves every hook git runs, so a value that leaves this directory skips the
hooks installed for the repository: `/dev/null` skips them all, and a directory elsewhere runs
whatever it holds. The audit therefore fails when `core.hooksPath` is set at any scope git reads
for the repository (system, global, local, worktree, and the `git -c` values that
`GIT_CONFIG_PARAMETERS` or `GIT_CONFIG_COUNT` hand the audit's own process) to a value that does
not name the managed hooks directory, even when that
other directory holds a known runner's hook. A relative value is resolved from the working tree
root, as git resolves it. An existing directory is compared by filesystem identity. A directory
not yet created is compared by its spelling, after the symlinks in its existing parent directories
are resolved on both sides. A checkout reached through a symlink, such as macOS's
`/var -> /private/var`, therefore passes with `.git/hooks` before that directory exists. A value
with a `..` element passes only when it names the existing managed directory, because the
operating system applies `..` after any symlink before it. The failure names each such value,
its scope and the file that sets it, and the command that removes it:

- a value in the file that `git config --<scope>` edits:
  `git config --unset-all --<scope> core.hooksPath`;
- a value in another file, one that an `include.path` or `includeIf.<condition>.path` names, or
  `$XDG_CONFIG_HOME/git/config` while `~/.gitconfig` exists: that file, and
  `git config --file "<file>" --unset-all core.hooksPath`. The `--<scope>` form edits one file
  only, `~/.gitconfig` for `--global` when it exists, and cannot remove the value;
- a `git -c` value: the option or the `GIT_CONFIG_PARAMETERS` or `GIT_CONFIG_COUNT` variable
  that passes it.

Where `lefthook.yml` exists and a local or global value from the file `git config --<scope>`
edits is refused, the failure also names `lefthook install --reset-hooks-path`. Lefthook 2.1.14 then unsets the
local and the global value (`unsetHooksPathConfig` in its
[install command](https://github.com/evilmartians/lefthook/blob/v2.1.14/internal/command/install.go))
and installs the hooks.

The rule is `auditHooksPath` in `internal/adopt/hooks_path_audit.go`. Adoption runs the same
rule before it installs a hook. On such a value it reports the audit's finding, fix included, as
an error, and installs no hook, in a dry run too. It no longer installs the hook in the directory
`core.hooksPath` names, where the next audit refused it. When git cannot read a value at all, for
example a `~user` path for a user that does not exist, adoption reports that read failure
instead, and installs no hook either. No supported runner installs anywhere
else: Lefthook 2.1.14 refuses to install while `core.hooksPath` is set globally, or locally to
anything but `.git/hooks`, and the pre-commit framework 4.6.2 refuses while it is set at all.

The audit reads the configuration that is in effect when it runs. A commit made with a one-off
`git -c core.hooksPath=...` override leaves no configuration behind, so CI re-runs the commit
checks over every commit of a pull request ([commit checks CI re-runs](#commit-checks-ci-re-runs)).

The audit recognises a hook by the marker its runner uses to recognise its own file:

| Runner | Marker | Configuration the runner reads |
| --- | --- | --- |
| Lefthook | the `LEFTHOOK` fingerprint Lefthook 2.1.14 checks for (`isLefthookFile`), together with its template's last line, `call_lefthook run "pre-commit"` | `lefthook.yml` |
| Praetor's fallback hook, which adoption writes when lefthook cannot install | `# praetor-managed pre-commit hook` (`fallbackPreCommitMarker` in `internal/adopt/hooks.go`) | `lefthook.yml` |
| The pre-commit framework | an ID the framework counts as its own hook's: the `# ID:` line pre-commit 4.6.2 writes, or an earlier template's (`is_our_script` in `pre_commit/commands/install_uninstall.py`) | `.pre-commit-config.yaml`, which must be the file the hook passes as `--config` |

The hook must also be runnable by git's own rule (`is_executable` in git's `run-command.c`).
On Linux and macOS, that means the owner execute bit is set. Windows has no execute bit, so the
file must start with `#!` there. Any other file fails the audit, and the failure names the path,
the file's size and its first line. A hand-written hook that runs the praetor commands also
fails: install the hook with a runner (`lefthook install`, `praetorctl adopt` or
`pre-commit install`).

When both configurations exist, the installed hook decides which one is checked. Lefthook's hook
and the fallback hook are checked against `lefthook.yml`, and `.pre-commit-config.yaml` is not
read. The framework's hook is checked against `.pre-commit-config.yaml`, even though
`lefthook.yml` exists. `standards_audit` and CI read no hook, so for them `lefthook.yml` alone
satisfies the gate.

A `.pre-commit-config.yaml` counts when its `repo: local` hooks run
`praetorctl compile-context --verify` and `praetorctl audit` at the `pre-commit` stage. Either
binary name counts, bare or by path, and `args` are appended to `entry`. If a hook sets no
`stages`, it takes `default_stages`, and if that is unset too, the hook runs at every stage.
`commit` is the old name for `pre-commit`. The file is read through `config.ReadYAMLDocument`,
which refuses a second document and duplicate keys. A configuration that passes:

```yaml
repos:
  - repo: local
    hooks:
      - id: praetor-compile-context
        name: praetorctl compile-context --verify
        entry: praetorctl compile-context --verify
        language: system
        pass_filenames: false
        always_run: true
      - id: praetor-audit
        name: praetorctl audit --offline
        entry: praetorctl audit --offline
        language: system
        pass_filenames: false
        always_run: true
```

Tests: `internal/adopt/hook_runner_audit_test.go` covers each runner's hook (positive), the
placeholder and the other hooks the audit refuses (negative), and both configurations together,
`core.hooksPath` and the Windows rule (boundary). `internal/adopt/hooks_path_audit_test.go`
covers the `core.hooksPath` rule: `/dev/null`, a directory outside the repository, one inside
the working tree and one beside the managed directory fail, each holding lefthook's hook, and so
do global, environment, included and `$XDG_CONFIG_HOME/git/config` values; an unset value and the
managed directory pass, under a symlinked checkout path too, before that directory exists. It
also runs the printed fix for an included value and an XDG one, and checks where the
`--reset-hooks-path` hint appears. The `TestAdopt_Hooks_Negative_*HooksPath*` tests in `internal/adopt/adopt_test.go` check
that adoption refuses the same values with the same finding as the audit that follows it.
`internal/adopt/precommit_config_audit_test.go`
covers the configuration rules. It also checks the hooks that `lefthook install` and
`pre-commit install` write, wherever those tools are on `PATH`.
`TestAudit_Boundary_HooksGate` and `TestAudit_Positive_PreCommitFrameworkRunner` in
`cmd/standardsctl/audit_cmd_test.go` and `TestServerAuditHookRunner_PreCommitFramework` in
`cmd/standards-mcp/audit_decline_test.go` run the same rules through both audits.

## Commit checks CI re-runs

The hooks prove nothing about a commit made without them: a one-off `core.hooksPath` override,
`--no-verify`, or a configuration the audit only reports afterwards. On every pull request, CI
therefore re-runs each commit check that needs nothing but the commit, over every commit of the
pull request (`.github/workflows/ci.yml`, the two steps after the documentation drift check):

| Hook check | Re-run in CI by | Test |
| --- | --- | --- |
| `commit-msg`: a conventional subject (`build`, `chore`, `ci`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`, `style` or `test`, an optional scope and `!`), or a subject Git wrote for a merge or a revert | `praetorctl forge check-commits --base=origin/<base> --head=HEAD` | `TestForgeCheckCommits_Negative_SubjectBreaksPolicy` |
| `commit-msg`: the HISS-14 `Migration:` footer on a breaking change | the same command | `TestForgeCheckCommits_EnforcesBreakingMigrationFooter` |
| `commit-msg`: the DCO sign-off | `scripts/dco_check.sh` in `.github/workflows/compliance.yml` | `scripts/test_dco_check.py` |
| `pre-commit`: whitespace errors and conflict markers (`git diff --cached --check`) | `python3 -B .config/lefthook/scripts/hooks.py commits origin/<base>`, which runs `git log --check` over the range | `CommitRange` in `.config/lefthook/scripts/test_hooks.py` |
| `pre-commit` and `pre-push`: private `.workingdir` content | the same stage, through `check_private_history` | `CommitRange` |

The commit-msg hook and CI share one implementation of the message policy, `forge.AnalyzeCommit`
(`internal/forge/commit_message.go`). The hook runs `praetorctl forge check-message <file>` on the
message file Git hands it, which first drops what Git removes before it records the message: the
lines that start with `#`, and the scissors line with everything after it. A message file over
1 MiB, or of more than 100000 lines before the scissors line, is refused, not checked in part
(`TestCleanCommitMessage_Boundary_LineBound`).

These checks are not re-run per commit:

- `block_evasion.py --environment` (`pre-commit`, `pre-rebase`) inspects the environment of the
  process that commits, which is gone once the commit exists.
- The live-state audit and `state sync --verify` (`pre-commit`, `commit-msg`) read the
  workstation's private `.workingdir` ledger, which no commit carries.
- The file checks on the exported index (Python and JSON syntax, the YAML, shell, workflow and
  Docker linters, gofmt, `go vet`, the agent instructions and the hook self-tests) need each
  commit's tree exported and scanned, once per commit. `make verify-all` runs gofmt
  (`fmt-check`), `go vet` (`lint`) and `compile-context --verify` on the pull request's head tree
  instead, so a defect that a later commit of the same pull request removes is not reported.

## Hook files adoption keeps

The audit compares none of the hook files below with a rendering, so `praetorctl adopt --force`
does not overwrite them. A file that still holds a text an earlier release wrote is refreshed on a plain run,
in its own line endings. An edited one is kept, `--force` included, with a warning reading
`not audit-verified; kept`. To regenerate it, delete it and re-run adopt.

| File | Unedited earlier text | Edited copy | Tests (`internal/adopt`) |
| --- | --- | --- | --- |
| `.config/agent/hooks/block_evasion.py` | refreshed to the current rendering (`priorEvasionHookDigests`, `testdata/evasion`) | kept | `TestAdopt_Positive_PriorEvasionHookRefreshedOnPlainRun`, `TestAdopt_Negative_EditedEvasionHookKeptUnderForce` |
| `.config/lefthook/scripts/checkpoint.py` and `common.py`, and `.config/lefthook/python.sh` | refreshed to the `--lock-source-root` bundle (`priorCheckpointDigests`, `testdata/checkpoint`) | kept; the checkpoint lifecycle is unavailable and no other bundle file is written | `TestReconcileCheckpointBundle_Positive_PriorScriptRefreshedOnPlainRun`, `TestReconcileCheckpointBundle_Negative_NoHalfRefreshBesideAKeptFile`, `TestAdopt_Boundary_ForceKeepsDriftedCheckpointScriptLifecycleUnavailable` |
| `.config/lefthook/engine.sh` ([the engine the hooks run](#the-engine-the-hooks-run)) | refreshed to the current text (`priorEngineLauncherDigests`) | kept | `TestAdopt_EngineLauncher_InstalledAndRefreshed`, `TestAdopt_EngineLauncher_EditedCopyKept`, `TestPriorEngineLauncherDigests_Boundary_CurrentTextRecorded` |
| the `pre-commit` hook in the directory git reports, written only when lefthook cannot install | not applicable | a hook praetor did not write is kept; the audit accepts it only when lefthook or the pre-commit framework wrote it ([the hook runner the audit accepts](#the-hook-runner-the-audit-accepts)) | `TestAdopt_Hooks_ForeignPreCommitKeptWithAndWithoutForce` |

Beside a `lefthook.yml` that extends the canonical policy, the interceptor and the checkpoint
scripts belong to that vendored bundle and are never refreshed
(`TestReconcileEvasionHook_Boundary_VendoredPriorKeptAndUnreadableUnverified`,
`TestReconcileCheckpointBundle_Boundary_VendoredPriorScriptKept`). Earlier releases renamed a
foreign `pre-commit` hook to `pre-commit.bak` under `--force`. Adoption now reports such a
file and leaves it in place (`TestAdopt_Hooks_LegacyPreCommitBackupReportedNotRemoved`).

Each set holds the current text too. After changing the interceptor template, an engine rule
it renders, or either checkpoint script, copy the new text under `internal/adopt/testdata/evasion`
or `internal/adopt/testdata/checkpoint` and add its digest to the set: the interceptor's is the
file `TestEmittedHookFixturesMatchTheRendering` regenerates.
`TestPriorEvasionHookDigests_Boundary_CurrentRenderingRecorded` and
`TestPriorCheckpointDigests_Boundary_CurrentSourcesRecorded` fail until the text is recorded,
so the next release still refreshes the copy adopters hold.

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

The diagnostics these scripts print are agent text, so `.config/lefthook/scripts`
and `.config/agent/hooks` are `register.sources` inputs in `.standards.yaml`. A
static message or f-string template is linted in the internal register. A value
that is entirely computed, such as a JSON result, passthrough tool output or one of
the markers above, carries one exact classification comment on its line:
`# caveman:not-applicable structured-protocol`, `untrusted-passthrough` or
`protocol-marker`. Changing a message changes the contract digest; refresh
`expected`, `not_applicable` and `sha256` from
`praetorctl caveman check --configured-sources --root=.`. The extraction rules are in
the [text-register guide](text-register.md#tracked-runtime-sources).

For Lefthook's full reporting on one run, set `LEFTHOOK_OUTPUT`, for example
`LEFTHOOK_OUTPUT=meta,summary,execution git commit -s`. The
[pinned Lefthook `output` reference](https://github.com/evilmartians/lefthook/blob/v2.1.14/docs/configuration/output.md)
lists the values. A personal `lefthook-local.yml` can set `output` or `colors`; it is
loaded after `extends` and wins.

## Remote checkpoints

The pre-push hook reserves a separate `checkpoint/*` namespace for unfinished audit work
(`push_check_mode` in `.config/lefthook/scripts/hooks.py`).
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
[`checks.py`](https://github.com/cordanaLLM/praetor/blob/main/.config/lefthook/scripts/checks.py) first runs `gate deadline --json`, which
resolves `PRAETOR_TEST_STAGE_TIMEOUT` through the pipeline's parser: the race-stage bound, clamped
to its 30-minute ceiling, once per test suite the repository holds, plus the allowance for the
other stages. It runs from the repository root, so a root holding both a `go.mod` and a
`Cargo.lock` gets the two-suite deadline `gate run` grants itself. It then adds a two-minute
launch margin (`GATE_LAUNCH_MARGIN`) for `go run` to rebuild the CLI. With the variable unset that
is 8 + 2 = 10 minutes; at the ceiling it is 37 minutes, so a raised bound takes effect on a push
(#314). A deadline report the hook cannot use fails the
push rather than running the gate under a guessed bound. The deadline itself is described in
[adoption verification](adoption-verification.md#the-whole-runs-deadline).

When that bound expires, or Ctrl-C interrupts the push, the hook stops the gate before it kills
it. `stop_process_group` in [`common.py`](https://github.com/cordanaLLM/praetor/blob/main/.config/lefthook/scripts/common.py) sends the
child's process group SIGTERM (SIGINT for Ctrl-C), waits up to `STOP_GRACE` (10 seconds) for every
member to exit, and only then sends SIGKILL; a second Ctrl-C during the wait kills at once. The
CLI runs each git and go command in a process group of its own and forwards a catchable signal to
those groups, waiting up to 5 seconds for them (`internal/util/command_interrupt_unix.go`), so git
removes its index lock and nothing is left running. `gate run` forwards SIGINT, SIGTERM and SIGHUP
the same way but does not end on them: it cancels the run, removes the race stage's isolated
worktree and its `wt/*` branch, and exits with the shell's 128+signal status (130, 143 or 129), not
the 1 of a rejection (`CancelCommandsOnSignal` in `internal/util/command_signal_context.go`). A
signal the CLI started with ignored, as under `nohup`, stays ignored. A Ctrl-C after the first
signal ends it at once; a repeated SIGHUP or SIGTERM does not, because closing a terminal sends
SIGHUP twice (the shell forwards it to its jobs, and the kernel sends it again when the shell
exits), and the cleanup the first one started is bounded.
`TestGateRun_Positive_TerminatingSignalCleansUpWorktreeAndBranch`,
`TestGateRun_Positive_RepeatedHangupStillCleansUp` and
`TestGateRun_Negative_IgnoredHangupKeepsTheRunGoing` in `cmd/standardsctl/signal_unix_test.go`
replay these cases. SIGKILL on the CLI's group cannot be forwarded
and misses those groups. On Linux the kernel still kills each command when the CLI dies
(`internal/util/command_parent_death_linux.go`); the processes a command started, and every
command on macOS, would run on. The `test_stop_*` cases in
[`test_hooks.py`](https://github.com/cordanaLLM/praetor/blob/main/.config/lefthook/scripts/test_hooks.py) replay the forwarded stop, the kill
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
untracked files, and OPEN, BACKLOG, BUGS and QUESTIONS bytes. STATE records
the observed Git state, clean flag and dirty count. `state status` reports an
inspection time, never a fabricated last-sync timestamp.

Only these canonical ledgers are read inside `.workingdir`; evidence, memories
and caches are excluded. Git-ignored public paths are also outside the binding.
The sync is bounded by bytes and time, not by repository size: 1 MiB per canonical
ledger, 16 MiB for the index listing, 8 MiB for every other Git output stream,
five-second Git probe deadlines, and at most 1,048,576 records per Git listing.
An untracked regular file of at most 1 MiB is bound by its bytes while 8 MiB of
untracked content remains; any other untracked path (a symlink, named pipe,
nested repository, larger file, or one past that budget) is bound by its type,
size and modification time instead of failing the sync. Missing or oversized
ledgers, irregular tracked files, unsupported submodules,
assume-unchanged/skip-worktree entries, and configured Git clean or process
filters fail explicitly and name the path. The
[state ledger integrity guide](state-ledger-integrity.md#what-the-sync-marker-binds)
lists every binding input and bound. These checks establish synchronized bytes;
they cannot establish that an agent recorded every relevant task or explanation.

The gate's untracked Exit-0 receipt `.standards-receipt.json` at the root is outside
the binding too, so a `praetorctl gate run` after the sync leaves the ledger current
(#136); the [state ledger integrity guide](state-ledger-integrity.md) states why.

This repository's native Stop/AfterAgent bridge calls the shared `agent-state-stop`
job and requires its unique success marker. Missing, stale or invalid state blocks
completion. The Git hooks rebuild the checkout CLI before ledger checks to avoid
accepting an older installed binary. Generated adoption bundles require their own
compatible CLI and hook integration; this repository's activation is not evidence
that every adopted client enforces state freshness.

The native pre-edit bridge (Claude `Edit`/`Write`, Gemini `replace`/`write_file`) calls the
shared `agent-checkpoint-pre-edit` job, `.config/lefthook/scripts/checkpoint_scope.py`, and
requires `PRAETOR_CHECKPOINT_SCOPE_OK`. Until a checkpoint is due it checks only the payload's
shape, so a path or session cwd outside the repository passes. Once one is due, both must sit
inside this repository (see [checkpoint cadence](checkpoint-cadence.md)), which the job decides
by filesystem identity rather than by how the client spelled the path. How each client
locates the bridges is described in
[agent hooks](agent-hooks.md#registrations-in-use-today).

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

A scoped run that takes the vulnerability check (`make check-changed` when no governance audit
runs, or `python3 .config/lefthook/scripts/hooks.py changed vuln <base>`) starts the
[Go vulnerability gate](adoption-verification.md#go-vulnerabilities-and-the-openvex-document)
with `go run ./cmd/standardsctl security govuln -- govulncheck`, over the whole module: a
module-level finding has no package to scope by. When the full gate runs, `gate run` makes the
same check.

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
configuration remain blocking failures; the sandbox gate does not bypass them.

The command guard accepts actual PreToolUse JSON or command arguments; Git calls
its explicit environment mode. It rejects verification-evasion commands and
hook exclusions, and an `adopt`, `conform`, `bootstrap` or `needs` command aimed
at the workstation dev root itself. It names no organisation folder: a command
aimed at `~/dev/<folder>` passes, and an operator who wants a folder refused adds a
pattern to `hooks.command_policy.deny`, which the Go hook entrypoint applies (see
[Agent hooks](agent-hooks.md#organisation-folders-dev-01);
`test_guard_blocks_the_dev_root_and_names_no_organisation_folder` in
`.config/lefthook/scripts/test_hooks.py`). It also refuses, without scanning, a command over 65,536
characters or with a line over 2,048 characters: Python's `re` backtracks, and a
longer line could hold the guard past the client's hook timeout (see
[Agent hooks](agent-hooks.md#the-adopted-interceptor);
`test_guard_refuses_commands_over_the_scan_bound_without_scanning` in
`.config/lefthook/scripts/test_hooks.py`). Split such a command, or write the long
content to a file first. A hook cannot intercept a Git invocation that disables all
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

This integration is checked against Codex CLI 0.145.0 and Lefthook 2.1.14.
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

The repository configures PostToolUse and Stop checkpoint hooks. They share
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

`gofmt_check()` is the one gofmt check. Pre-commit runs it on the staged set, and `make
fmt-check` (`hooks.py fmt-check`), which `make lint` depends on and the CI gofmt step calls, runs
it on every tracked Go file. It hands gofmt at most `GOFMT_BATCH` files per run, so a whole-tree
check stays under the Windows command-line limit
(`test_fmt_check_skips_testdata_and_names_unformatted_source` and
`test_gofmt_check_batches_cover_every_file` in `.config/lefthook/scripts/test_hooks.py`).

This is not a convenience. The HISS-20 corpus under `.config/hiss/testdata/` exists *because* its
positive fixtures violate an invariant — a file that fails to be a bounded loop is how the rule is
proven to fire. Scanning them reports the engine's own test data as the repository's debt and blocks
every push that touches the corpus. The HISS scanner, the dedupe scan, gitleaks and `make
fmt-check` all skip the same directory name, which is Go's own convention for the same reason.

The semgrep stage has two forms and both apply the exclusion. A per-file scan drops fixture paths
from its file list. When a pushed range touches `.config/semgrep/`, the scan widens to the whole
tree and passes `--exclude testdata` to semgrep instead, because a bare `.` would walk into the
corpus. Both forms read the directory name from `FIXTURE_DIRECTORY`, so they cannot disagree.

## Which changed files select which checks

`.config/lefthook/scripts/checks.py` classifies each changed path once, and each set is held to its
real source by a test in `.config/lefthook/scripts/test_hooks.py`:

| Set | Selects | Matches | Held in step by |
| :--- | :--- | :--- | :--- |
| `CONTEXT`, `CONTEXT_PREFIXES` | `compile-context --verify` | `AGENTS.md`, `.agents/**`, the six vendor files, the `.claude/`, `.codex/`, `.gemini/`, `.github/` `agents/` persona directories and the `.claude/skills/` copies of the register skills | `test_context_changed_covers_every_compile_context_path` runs the real `compile-context` and requires every file it reads or writes to match |
| `GO_EXTENSIONS` | the Go packages to build, test, lint and scan | every suffix `go/build` compiles into a package: `.go`, cgo C/C++/Objective-C sources and headers, assembler (`.s`, `.S`, `.sx`), Fortran, SWIG and `.syso` | `test_go_packages_selects_cgo_and_assembler_inputs` |
| `SEMGREP_SUFFIXES` | the per-file semgrep scan | the extensions semgrep assigns each language in `.config/semgrep/hiss-invariants.yml`, including `.h`, `.hpp`, `.cc`, `.jsx` and `.tsx` | `test_semgrep_suffixes_cover_every_rule_language` fails when a rule names a language the table lacks |

Before these sets were widened, a commit touching only a persona or skill skipped
`compile-context --verify`, and headers, C++ and JSX/TSX sources never reached the semgrep rules
written for them. A Go package directory `go list` reports outside the snapshot is a hook error
naming it, not a Python traceback.

## Running the gate on Windows

A Windows checkout depends on four platform behaviours. Each one blocks `git commit` if it is
missing, so all four must hold. Two programs must also prove themselves there: the hooks'
interpreter, which Windows does not reliably provide under the name `python3`
([the interpreter the hooks run](#the-interpreter-the-hooks-run)), and GNU Make
([the make the hooks run](#the-make-the-hooks-run)).

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

Five POSIX-only calls in the scripts Lefthook invokes failed on Windows, each of them after the
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
documents — `message`, `template`, `merge`, `squash`, `commit` — count as a source.
