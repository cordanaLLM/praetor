# Workstation sync with devsync

`praetorctl devsync` copies a development workstation's project folders to Google Drive
through [rclone](https://rclone.org) and brings them back on another machine. It is a
stopgap for moving between workstations: it keeps one copy per workstation, no history,
and never merges. Code lives in `internal/devsync/`; the command in
`cmd/standardsctl/devsync.go`.

Everything is stored encrypted through an rclone `crypt` remote named `praetor-sync`,
which writes into the folder `praetor-sync/` at the root of your Drive. Each workstation
gets one subfolder, named after its host name.

## Before you start

1. Install rclone and create a Google Drive remote named `gdrive` with `rclone config`.
2. Use your own Google client ID for that remote. rclone's shared client ID is being
   retired and stops working during 2026; see
   [Making your own client_id](https://rclone.org/drive/#making-your-own-client-id).

## Set up once per workstation

On the first workstation, create the encrypted remote:

```sh
praetorctl devsync init
```

This runs `rclone config create praetor-sync crypt remote=gdrive:praetor-sync ...`
with two freshly generated keys, passed with `--obscure` so rclone stores them obscured.
It refuses to run when a remote named `praetor-sync` already exists.

Every workstation needs the same remote with the same keys. Do not run `init` on the
others, because that would generate different keys. Instead, copy the `[praetor-sync]`
section of the first workstation's rclone config file (`rclone config file` prints its
path, `rclone config show praetor-sync` prints the section) into the rclone config of each
other workstation. Keep the keys safe: anyone holding them can read the archives, and
without them nothing can be restored.

`--remote-name` and `--base` choose another remote name or folder.

## Push

```sh
praetorctl devsync push            # add --dry-run to see what would be uploaded
```

Push looks through the dev folder and writes one `.tar.gz` archive per unit. The dev
folder is `--dev`, otherwise `$PRAETOR_DEV_ROOT`, otherwise the earlier
`$PRAETOR_DEV_DIR`, otherwise `~/dev`; with none of them and no usable home directory
push fails and names `--dev` (`resolveDevRootDir` in `cmd/standardsctl/devroot.go`).

| Unit | Archive on the remote |
| :--- | :--- |
| each git repository up to five levels below the dev folder, such as `org/a/b/c/repo` | `<host>/dev/<path>.tar.gz` |
| each top-level folder that is not a repository, without the repositories inside it | `<host>/dev/<folder>.tar.gz` |
| the agent state bundle (`praetorctl harvest bundle`) | `<host>/agent-state.tar.gz` |

Archives include `.git` and leave out build caches: `node_modules`, `target`, `.venv`,
`venv`, `__pycache__`, `.cache`, `dist`, `build`, `.next`, `.svelte-kit` and `.gradle`.
Inside a repository, such a folder is left out only when git ignores it as a whole
(one `git ls-files --others --ignored --exclude-standard --directory` call per
repository), so a `build/` or `dist/` holding tracked files is archived. When git cannot
answer, the push line notes it and the folders are left out by name. Top-level folder
archives always leave them out by name. Loose files directly in the dev folder, sockets
and devices are not copied.

Push remembers the file count, newest modification time and total size of each archive
in `<user config dir>/praetor/devsync-state.json` and skips archives whose contents have
not changed. Delete that file to upload everything again. The agent state bundle is
uploaded on every push. It is encrypted like every other archive, and its local
temporary copy is deleted after the upload. Shell history is left out. Agent and MCP
configuration JSON is redacted before it enters the bundle: the value of every
credential-named key (`token`, `secret`, `password`, `apiKey`, `Authorization` and
similar), every string in an `env` object, and every string carrying a bearer or basic
credential is replaced with `"[REDACTED]"`, and every other byte is copied unchanged. A
configuration JSON file that does not parse is skipped with a note rather than copied.
Non-JSON configuration such as Codex's `config.toml` is still copied as written, so the
bundle can still carry credentials; `praetorctl harvest bundle` prints the redaction
count beside the credential-bearing categories it captured. The rules live in
`internal/harvester/redact.go` and are pinned by `internal/harvester/redact_test.go`.

An upload goes to `<archive>.partial` first and replaces the previous archive only when
it is complete, so a failed push leaves the last good copy in place. Each archive prints
one line, `uploaded`, `skipped` or `failed`, with its size; the command exits non-zero
when any archive failed. A folder name containing a shell metacharacter such as a quote,
`$` or a parenthesis is reported as failed rather than passed to rclone.

### Size cap

`--max-archive-size` (default `2GiB`) skips an archive whose source measures larger than
the cap, so one oversized project folder cannot balloon a push into hundreds of gigabytes.
Because the archive is streamed straight to the remote, push decides before writing a
single byte of it: it reuses the same measurement already described above (file count,
newest modification time, total size), the one recorded in the state file, rather than
buffering an archive just to weigh it.

A skipped archive prints one line, `too-large`, with its measured size and the cap, exactly
like the `uploaded`, `skipped` and `failed` lines; `--dry-run` reports the same skip. Push
always ends with a summary line stating how many archives it skipped for size and their
combined size, so nothing is left off the remote without saying so. Skipping for size is
not a failure and does not change the command's exit code.

Accepted sizes are a plain number of bytes, or a number followed by a binary unit —
`B`, `KiB`, `MiB`, `GiB` or `TiB`, matched case-insensitively, for example `2GiB` or
`500MiB`. `0` or `none` (any case) disables the cap.

To raise or remove the cap for one push:

```sh
praetorctl devsync push --max-archive-size=20GiB   # raise it
praetorctl devsync push --max-archive-size=none    # disable it
```

`--host` overrides the host name; `--remote` another remote.

## Pull

```sh
praetorctl devsync pull --host=<workstation>
```

Pull downloads every archive of that workstation and unpacks it below
`<target>/<host>/`, mirroring the remote layout: `<host>/dev/org/repo.tar.gz` becomes
`<target>/<host>/dev/org/repo/`. The default target is `<user data dir>/praetor/devsync`
(`~/.local/share` or `$XDG_DATA_HOME` on Linux, `~/Library/Application Support` on
macOS, `%LOCALAPPDATA%` on Windows); `--into` chooses another.

Pull never overwrites working copies. It refuses a target inside the dev folder (resolved
as for push, without `--dev`) and one that already holds files. Move the restored
projects into place yourself. While unpacking, every entry must stay inside the target:
absolute names, `..` segments, hard links and symbolic links that point outside the
target or through another link are refused. Restoring an archive that contains symbolic
links on Windows needs Developer Mode or an elevated prompt.

## List

```sh
praetorctl devsync ls
```

Lists every workstation's archives with size and time.

## Testing and other rclone setups

`--rclone-config` selects the rclone config file, and `PRAETOR_RCLONE` selects the rclone
binary. `--remote` also accepts a plain directory path. The tests in
`internal/devsync/rclone_test.go` use exactly this: a temporary config file and a local
directory as the crypt base, so they never read your rclone configuration or reach Drive.
They are skipped when rclone is not installed; `internal/devsync/fake_rclone_test.go`
covers the same logic with a stand-in rclone on every platform.
