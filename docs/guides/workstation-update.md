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

`scripts/dev_install.py` (`make dev-install`) is a thin wrapper around the same command:
it runs the MCP functional probe first, then calls `workstation install --source <this
checkout>` (HISS-19 — the atomic install exists in one place, not two). See
[Develop against this checkout's MCP](development-mcp.md) for that workflow.

## `workstation install`

```text
praetorctl workstation install --source PATH [--bin-dir PATH] [--manifest PATH]
                                [--fleet-config PATH] [--workstation-config PATH]
```

| Flag | Meaning |
| --- | --- |
| `--source` | Checkout to build the three binaries from. Required; there is no default. |
| `--bin-dir` | Destination directory. Default: the loaded `update.bin_dir` setting, else the per-OS default. |
| `--manifest` | Install manifest path. Default: the per-user configuration directory (below). |
| `--fleet-config`, `--workstation-config` | Layered settings documents (`docs/guides/workstation-settings.md`). Loading one requires `--source` to be a governed checkout (it carries the `.standards.yaml` the loader reads). Neither is required: a plain `install --source .` never touches a settings document. |

Steps, in order (`internal/workstation/install.go`):

1. **Lock.** An exclusive lock directory (`.praetor-workstation-install.lock` under
   `--bin-dir`) serializes concurrent installers; a second install while one is running
   is refused rather than merged.
2. **Inspect.** Every installation target (each binary and its alias) is classified
   before anything is written. A symlink this engine did not create, or a non-regular
   file (a directory, device or pipe) at a target, is refused untouched.
3. **Back up.** Anything already at a target is copied into a fresh backup directory
   under `--bin-dir`, skipped entirely on a genuine first install (nothing to back up).
4. **Build and place.** Each binary is built with `go build -trimpath` (so `praetorctl
   version` still reports the exact built commit — `-trimpath` strips source paths, not
   Go's VCS stamp) into a scratch directory, then staged beside its destination and
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
| `lock_held` | Whether an installation lock is currently held in the bin directory (`--bin-dir`, or the manifest's own `bin_dir` when not given). Status never takes the lock itself. |
| `clients` | Per-client configuration-root presence, resolved through C1 (`internal/clientsetup/roots.go`, `clientsetup.Root`). Every known client has a root entry; a client whose resolution fails, for example a relocation variable naming a missing directory, reports a stated reason instead of a false negative; `--home` selects a foreign home directory for this resolution, the same override `harvest bundle --home` uses. |

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
later changes. Until then, refresh an install by running `workstation install` again
with the same `--bin-dir`: the manifest records the prior commit and a backup for the
next command to build a rollback on top of.
