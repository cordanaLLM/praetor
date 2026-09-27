# Workstation install and status

`praetorctl workstation` is the one engine command a workstation runs to install and
inspect this engine, on every operating system (`cmd/standardsctl/workstation.go`,
`internal/workstation/`). `install` and `status` ship now; `update`, `rollback` and
`schedule render` are a later change (W2, W3) and are not implemented yet.

## Quick start

```bash
praetorctl workstation install --source /path/to/praetor/checkout
praetorctl workstation status
```

`install` builds `praetorctl`, `praetor-mcp` and `praetor-lsp` from the given checkout
and places them, with their legacy aliases (`standardsctl`, `standards-mcp`,
`standards-lsp`), into a bin directory — `~/.local/bin` on Linux and macOS,
`%LOCALAPPDATA%\Programs\praetor` on Windows by default (the per-OS default in
`internal/clientsetup/roots.go`, `LocationBinDir`). `status` reports what is installed
there without changing anything.

Both commands take flags in any order and no positional argument. A stray positional is
refused, and so is any token after a `--` terminator, since everything after `--` is
positional (`parseInterspersed`, `cmd/standardsctl/flagargs.go`).

`scripts/dev_install.py` (`make dev-install`) is a thin wrapper around the same command:
it runs the MCP functional probe first, then calls `workstation install --source <this
checkout>` (HISS-19 — the atomic install exists in one place, not two). See
[Develop against this checkout's MCP](development-mcp.md) for that workflow.

## `workstation install`

```text
praetorctl workstation install --source PATH [--bin-dir PATH] [--manifest PATH] [--if-stale]
                                [--fleet-config PATH] [--workstation-config PATH]
```

| Flag | Meaning |
| --- | --- |
| `--source` | Checkout to build the three binaries from. Required; there is no default. |
| `--bin-dir` | Destination directory. Default: the loaded `update.bin_dir` setting, else the per-OS default. |
| `--manifest` | Install manifest path. Default: the per-user configuration directory (below). |
| `--fleet-config`, `--workstation-config` | Layered settings documents (see [effective policy](effective-policy.md#which-documents-the-hook-client-and-workstation-commands-use)). Loading one requires `--source` to be a governed checkout (it carries the `.standards.yaml` the loader reads). Neither is required: a plain `install --source .` never touches a settings document. |
| `--if-stale` | Refresh an existing install only when it lags `--source`; otherwise install nothing and print why. See [Refresh a lagging install](#refresh-a-lagging-install). |

Steps, in order (`internal/workstation/install.go`):

1. **Lock.** An exclusive lock directory (`.praetor-workstation-install.lock` under
   `--bin-dir`) serializes concurrent installers; a second install while one is running
   is refused rather than merged.
2. **Inspect.** Every installation target (each binary and its alias) is classified
   before anything is written. A symlink this engine did not create, or a non-regular
   file (a directory, device or pipe) at a target, is refused untouched.
3. **Back up.** Anything already at a target is copied into a fresh backup directory
   under `--bin-dir`, skipped entirely on a genuine first install (nothing to back up).
4. **Build and place.** Each binary is built with `go build -trimpath -buildvcs=true` into
   a scratch directory. `praetorctl version` still reports the exact built commit:
   `-trimpath` strips source paths, not Go's VCS stamp, and `-buildvcs=true` overrides a
   `GOFLAGS=-buildvcs=false` that would leave the install unstamped and outside the
   [engine build check](#engine-build-check). When no tracked file differs from HEAD, the
   build reads a fresh clone of the HEAD commit (`prepareBuildSource`,
   `internal/workstation/source.go`), so untracked files such as the gate receipt neither
   enter the binaries nor mark them `-dirty`; Go stamps `vcs.modified` from
   `git status --porcelain`, which lists untracked files. A checkout with a modified or
   staged tracked file is built as it stands and stamped `-dirty`, so a work-in-progress
   install carries its edits. Each binary is then staged beside its destination and
   swapped into place with one rename, so the replacement is atomic. Windows cannot
   rename a new file onto one that is memory-mapped for execution — the running
   `praetorctl` itself — so there the previous file is renamed aside first, freeing the
   name, before the new one takes it (`swapInto`, `internal/workstation/binaries.go`).
   Every build/placement failure restores everything this run already touched from the
   backup.
5. **Manifest.** The install manifest (below) is written, recording the built commit,
   each binary's digest, the previous install when there was one, and which settings
   documents (if any) were used.

An installed binary's existing file permissions are never silently widened: if a target
was already chmod'd non-executable, the next install keeps it that way and refuses
rather than guessing that was accidental.

## `workstation status`

```text
praetorctl workstation status [--source PATH] [--bin-dir PATH] [--manifest PATH] [--home PATH]
```

Prints one JSON object (`internal/workstation/status.go`):

| Field | Meaning |
| --- | --- |
| `installed`, `manifest` | Whether a manifest exists at `--manifest`, and its contents when it does. |
| `manifest_sha256` | Digest of the manifest file itself, for external tooling to reference an exact reported state. |
| `checkout_head`, `up_to_date` | `--source`'s current commit, and whether it equals the installed `engine_commit`. Omitted when `--source` is not given. |
| `commits_behind` | How many commits `--source`'s HEAD is ahead of the installed `engine_commit`: `0` at HEAD. Omitted when `--source` is not given, and when the installed commit is not an ancestor of HEAD or not in the checkout at all (`workstation.InstallLag`, `internal/workstation/refresh.go`). |
| `lock_held` | Whether an installation lock is currently held in the bin directory (`--bin-dir`, or the manifest's own `bin_dir` when not given). Status never takes the lock itself. |
| `clients` | Per-client configuration-root presence, resolved through C1 (`internal/clientsetup/roots.go`, `clientsetup.Root`). Every known client has a root entry; a client whose resolution fails, for example a relocation variable naming a missing directory, reports a stated reason instead of a false negative; `--home` selects a foreign home directory for this resolution, the same override `harvest bundle --home` uses. |

## Refresh a lagging install

Client wrappers and agent hooks run the installed `praetorctl`, not the checkout's build, so
an install left at an old commit keeps serving old behavior after the checkout moves on.
`workstation install --if-stale` refreshes it (`workstation.Refresh`,
`internal/workstation/refresh.go`):

```bash
praetorctl workstation install --source /path/to/praetor/checkout --if-stale
```

It rebuilds only when every condition holds, checked in this order, and otherwise installs
nothing and prints the first reason that failed:

1. `--source` declares the running engine's own module in its `go.mod`. Any other checkout
   is skipped before the manifest or a settings document is read.
2. An install manifest exists (`--manifest`, else the default path). A refresh never makes
   a first install.
3. The checkout is on the update branch: `update.branch` from the selected operator
   settings, `main` by default ([effective policy](effective-policy.md)).
4. The installed `engine_commit` is a strict ancestor of the checkout HEAD, so a refresh
   never downgrades or moves sideways.
5. No tracked file is modified. Untracked files do not block a refresh, and they stay out
   of the build: the refresh builds a clean clone of HEAD (install step 4), so the refreshed
   install passes the [engine build check](#engine-build-check).

A refresh installs into the manifest's `bin_dir` unless `--bin-dir` names another, and
records the same settings documents again unless `--fleet-config` or `--workstation-config`
names another; the environment variables are not consulted, so a refresh reinstalls what was
installed. It prints one JSON object: `refreshed`, `reason`, `commits_behind` and, after a
rebuild, `install` (the same report `install` prints).

This repository's `post-merge` hook runs it after every merge
(`refresh_install` in `.config/lefthook/scripts/hooks.py`, see [Git hooks](git-hooks.md)): a
skip prints nothing, a rebuild prints one line. A native scheduler can run the same command.

## Engine build check

`praetorctl compile-context` and the MCP `standards_compile_context` write refuse to write
when the running engine does not match the engine checkout they write into
(`workstation.CheckBuildCurrent`, `internal/workstation/freshness.go`). Without it, a client
wrapper that runs `praetorctl compile-context` at session start with a lagging install
rewrote the `AGENTS.md` register block and every vendor file with that install's older text.

The check applies only when the target directory's `go.mod` declares the binary's own main
module and the binary carries Go's VCS stamp (`praetorctl version`). Every other repository,
and a `go run` build, which compiles the checkout on the spot, is not judged. A stamped build
matches when:

- it is a clean build and no non-test `.go` file, `go.mod` or `go.sum` differs between its
  revision and the working tree, committed or untracked; or
- it was built from a modified tree (`-dirty`), its executable lies inside the checkout (for
  example `bin/praetorctl`), and every changed input is older than the executable.

Go marks a build `-dirty` for an untracked file alone, so both builders that feed this check
avoid a `-dirty` build outside the checkout when nothing tracked changed:
`workstation install` builds a clean clone of HEAD (install step 4), and
`scripts/dev_mcp.py` builds under the checkout's git-ignored `bin/` (`build_directory`).
A work-in-progress install, built from modified tracked files into the bin directory, is
refused.

Anything else fails with one line naming the build, the reason, and the command that writes
with the checkout's own compiler:

```text
compile-context wrote nothing: engine build does not match this checkout: build 0123456789ab lacks 3 changed Go build inputs (first internal/compiler/render.go); rebuild bin/praetorctl from the checkout or run go run ./cmd/standardsctl compile-context
```

`compile-context --verify` and `verify_only` never write and are never refused. Tests:
`internal/workstation/freshness_test.go`, `internal/workstation/source_test.go` (a real
refresh from a checkout holding an untracked receipt passes the check),
`cmd/standardsctl/compile_context_engine_test.go`,
`cmd/standards-mcp/compile_context_engine_test.go`, `BuildDirectoryTests` in
`scripts/test_dev_mcp.py`.

## The install manifest

`internal/config/install_manifest.go` (S1) defines and validates the schema;
`internal/workstation` is the only writer (`config.WriteInstallManifest`), staged and
renamed into place so a concurrent reader never observes a partial file. Default location:
`os.UserConfigDir()/praetor/install.json` (`~/.config/praetor/install.json` on Linux,
honoring `XDG_CONFIG_HOME`; `%APPDATA%\praetor\install.json` on Windows) — the same
per-user configuration directory as the receipt signing key
(`internal/lockdown/keys.go`). It is never committed and is git-ignored.

`hook`, `clients *` and `workstation *` resolve settings documents in order: an explicit
flag, then `PRAETOR_FLEET_CONFIG` / `PRAETOR_WORKSTATION_CONFIG`, then the paths this
manifest recorded (rejected if the file's digest no longer matches — `config.SelectOperatorSettings`).
`audit` and `gate` never read this manifest; they take explicit flags only.

## Not yet implemented

`workstation update`, `workstation rollback` and `workstation schedule render` land in
later changes. Until then, refresh an install with `workstation install --if-stale`
([above](#refresh-a-lagging-install)) or by running `workstation install` again with the
same `--bin-dir`: the manifest records the prior commit and a backup for the next command
to build a rollback on top of.
