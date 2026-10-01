# Portable DevContainer bootstrap

New DevContainers use an explicitly selected Praetor source snapshot. Adopted
repositories do not need Praetor's `cmd/standardsctl` sources or
`docker/dev/Dockerfile` in their own project tree.

```bash
praetorctl devcontainer generate \
  --source-root /path/to/reviewed/praetor \
  --config .standards.yaml \
  --output .devcontainer/devcontainer.json

praetorctl devcontainer verify
```

Generation prepares the configuration and exact companion files. It does not
build an image, execute the selected source, or certify application tests.
The selected source must be a Git checkout declaring the Praetor module. From its
tracked and nonignored untracked files, preparation captures what
`go build ./cmd/standardsctl` reads: the Go sources of every module package
`cmd/standardsctl` imports, directly or transitively, plus `go.mod`, `go.sum`,
`LICENSE`, and the assets of each reached `go:embed` family — every managed
asset family in the registry (`managedasset.Families`,
`internal/managedasset/family.go`; today the five declared `tools/markdownlint`
assets) and the `templates/*/*.tmpl` bodies flavor apply scaffolds. The import
closure is read with Go's parser, not the go command, so preparation needs no Go
toolchain (`captureBootstrapClosure` in
`internal/devcontainer/bootstrap_closure.go`). Build constraints are not
evaluated: a reached package contributes every non-test file and its imports,
for every platform. Packages only other commands import, such as
`cmd/standards-mcp`, stay out, and an import of a module package with no
captured Go source fails preparation, because the image could not build it.
The set is captured twice; a changed snapshot fails preparation. Go's test surface,
`_test.go` files and anything under a `testdata` directory, is not captured:
the generated Dockerfile only runs `go build ./cmd/standardsctl`, which reads
neither. The Git pathspec excludes it and the name check refuses it first, for
declared assets too, through one rule, `util.IsGoTestSurface`
(`internal/util/gosource.go`), so a test file cannot enter a captured set, and
an archive carrying one fails verification
(`internal/devcontainer/bootstrap_source.go`). A captured set holding a family's
embedding source, such as `tools/markdownlint/assets.go` or `templates/embed.go`,
must hold every asset that source embeds, and each source may carry only its
declared directive (`bootstrapAssetFamilies`). Undeclared
`go:embed` inputs and unsupported native build inputs fail explicitly rather
than being omitted. Capture is bounded to 4,096 files, 8 MiB total, and 1 MiB
per file.

The recorded `customizations.praetor.bootstrap` specification identifies the
source snapshot, compressed archive, Dockerfile, and immutable builder/base
images. The compiled CLI retains the version declared by the selected source;
its displayed version is not the bundle digest. Read the source identity from
the retained bootstrap specification and verified companions. The compressed
source is carried in at most eight 512 KiB base64 files (`maxBootstrapParts`
in `internal/devcontainer/bootstrap.go`), alongside
`Dockerfile.praetor`. Keep these files together with the JSON. They are
source-bearing artifacts: select and review the source before copying a bundle
to another repository. The hashes establish integrity, not source authenticity
or a release signature.

`TestRepositoryBootstrapSourceKeepsHeadroom`
(`internal/devcontainer/bootstrap_source_test.go`) measures Praetor's own
capture against the frame capacity, the 8 MiB total and the file count. It
fails above 80% of any of them, so growth is reported before a bootstrap stops
fitting; run it with `go test -v` to print the current usage.

The Dockerfile verifies the archive, checks module integrity without changing
`go.mod` or `go.sum`, and builds the selected CLI. The runtime installs it at
`/usr/local/bin/praetorctl`, plus a `standardsctl` compatibility name. Startup
uses that absolute CLI to compile the agent context, then invokes
`/usr/bin/make verify-all`. Native application tools must still be supplied by
the selected DevContainer features or a reviewed custom container.

To exercise the prepared Dockerfile independently of an IDE:

```bash
docker build --file .devcontainer/Dockerfile.praetor \
  --tag praetor-bootstrap-local .devcontainer
```

Run the generated `postCreateCommand` against a disposable project checkout
when testing startup. A successful configuration verification checks the exact
recorded companions; a successful image build and successful startup are
separate evidence. Verification uses the retained specification, so it does
not require the original workstation source path or a hardcoded generator
commit to remain available.

Verification compares `devcontainer.json` itself against the render of the
expected configuration, after normalising CRLF line endings to LF so a Windows
checkout with `core.autocrlf=true` does not report that file as drift (HISS-21).
JSON forbids an unescaped carriage return inside a string, so every CRLF in the
file is whitespace between tokens and the normalisation cannot hide an edit.
The normalisation covers `devcontainer.json` only. Companions such as
`Dockerfile.praetor` are checked against their recorded hashes as raw bytes,
and adoption does not yet write a `.gitattributes` pin for `.devcontainer/`
([#313](https://github.com/cordanaLLM/praetor/issues/313)). Until it does, a
CRLF checkout of a ready bootstrap still fails verification on
`Dockerfile.praetor`; add `.devcontainer/* text eol=lf` to the adopted
repository's `.gitattributes`, as Praetor does for itself (`.gitattributes:51`).
Verification does not compare a re-render of what decoded, because
Go's JSON decoder matches member names case-insensitively and keeps the last of a
duplicate pair: `POSTCREATECOMMAND`, `RemoteUser` and a repeated
`postCreateCommand` all decode into the managed struct and re-marshal to the
spec spelling, while the DevContainer runtime reads object keys case-sensitively
and would run none of them. A key outside the managed schema, such as a
hand-added `initializeCommand` or `runArgs`, is named in the rejection; every
other edit, including whitespace and content after the configuration object, is
reported as drift. Adoption preserves an existing custom DevContainer and
reports it as execution-unverified, but `standardsctl audit` verifies any
`.devcontainer/devcontainer.json` against the declared standards
(`cmd/standardsctl/audit.go`, `auditAgentContextAndDevcontainer`). A file
carrying keys outside the managed schema therefore fails audit; no
configuration keeps such keys and passes it.

Without `--source-root`, or with an explicitly selected config-only catalog,
generation writes an `unavailable` configuration and returns an error. Its
startup fails with an actionable message; it cannot report a missing CLI as
ready. To remediate, rerun the same command with `--source-root`. It replaces
the placeholder without `--force` while the file is exactly what Praetor
rendered for the same profiles and features, CRLF line endings aside; any edit
keeps the file behind `--force` (`admitReplacement` in
`internal/devcontainer/bootstrap_io.go`, tests in
`internal/devcontainer/bootstrap_replace_test.go`). `--force` without
`--source-root` never replaces a ready bootstrap with a placeholder: the
configuration would stop starting and `Dockerfile.praetor` and the source parts
would be left orphaned. Select a source root, or remove the bundle files first
to drop the bootstrap deliberately. Invalid explicit source paths, mutable image references, incomplete
bundles, symlinks, and altered companions are errors. Optional `--builder-image`
and `--base-image` overrides must name a lowercase SHA-256 digest, as
`repository@sha256:<digest>` or `repository:tag@sha256:<digest>`; a tag alone or
a malformed digest is refused (`validateBootstrapImages` in
`internal/devcontainer/bootstrap.go`, tests in
`internal/devcontainer/bootstrap_digest_test.go`).

The reviewed defaults, `DefaultBuilderImage` and `DefaultBaseImage` in
`internal/devcontainer/bootstrap.go`, are digest-only references. The comment
beside each keeps the full `repository:tag@sha256:<digest>` reference it was
reviewed at, and `TestReviewedDefaultCommentsNameTheirDigest` holds that comment
to the constant's digest. `@devcontainers/cli` 0.89.0 refuses
a `repository:tag@sha256:<digest>` reference while it inspects the registry
(devcontainers/cli#1307), so it cannot build a bundle that records one
([#333](https://github.com/cordanaLLM/praetor/issues/333)). Praetor still
accepts a tagged override; give the digest-only form to a bundle that CLI builds.

Regeneration keeps the images the output file records. Without `--base-image`
or `--builder-image`, generation reads the bootstrap specification already
recorded there. A recorded image that is an earlier reviewed default, such as
the `ubuntu-24.04` base Praetor shipped before the 26.04 move, is refreshed to
the current reviewed pin. So is the current default recorded with a tag, such as
the `ubuntu26.04` form generation recorded before the defaults became
digest-only. Earlier defaults are listed as `repository@digest` in
`priorDefaultBaseImages` and `priorDefaultBuilderImages` in
`internal/devcontainer/bootstrap.go`, and a recorded image matches the current or
an earlier default when it names the same repository and digest, under any tag
or none (`isReviewedPin` in `internal/devcontainer/bootstrap_recorded.go`). Any
other recorded image is the adopter's choice and is kept, so `--force` does not
swap it for the reviewed default. That includes another digest of the reviewed
default repository, such as a `debian-12` base or a newer `golang` builder, and
the reviewed digest under another repository, such as a mirror. An explicit flag
always wins.
Each image kept, refreshed or replaced against its recorded value is printed as a `[RECORDED IMAGE KEPT]`, `[RECORDED IMAGE REFRESHED]` or
`[RECORDED IMAGE REPLACED]` line naming the recorded image, the selected one and
the flag that changes it. A missing file, a custom DevContainer or an invalid
specification records no choice, so the reviewed defaults apply
(`InheritRecordedImages` in `internal/devcontainer/bootstrap_recorded.go`,
tests in `internal/devcontainer/bootstrap_recorded_test.go` and
`TestDevContainerCLIForceKeepsRecordedBaseImage` in
`cmd/standardsctl/devcontainer_bootstrap_test.go`).

Adoption uses its explicit `--lock-source-root` as the bootstrap source and the
same planned or preserved manifest that audit consumes. A config-only catalog
can supply governance policy while bootstrap remains visibly unavailable.
Dry-run lists planned companion paths without writing them. Existing custom
DevContainers are preserved and reported as execution-unverified. Forced
adoption keeps recorded images by the same rule and lists each note as a
warning.

## Bundle freshness

`praetorctl devcontainer verify` checks a bundle against its own records, so a
bundle cut a hundred commits ago verifies as cleanly as one cut a minute ago
([#338](https://github.com/cordanaLLM/praetor/issues/338)). The freshness check
compares it with the tree instead:

```bash
praetorctl devcontainer freshness
```

It recaptures the build source from the working tree beside `--config` with the
capture generation uses, and compares its digest with the recorded
`sourceSHA256`. Equal digests print `[FRESH]`. Otherwise the check finds the
newest commit reachable from `HEAD` that changed a bundle file
(`devcontainer.json`, `Dockerfile.praetor` or a `praetor-source.*.b64` part),
counts the commits from it to `HEAD`, and measures its committer age. Inside the
bounds it prints both numbers under `[DRIFT WITHIN BOUNDS]` and passes; past
either bound it fails with them and the regeneration command. A bundle exactly
at a bound passes (`CheckFreshness` in
`internal/devcontainer/bootstrap_freshness.go`).

The bounds are declared in `.standards.yaml`; an unset key keeps its default:

```yaml
devcontainer:
  freshness:
    max_commits: 500
    max_age_days: 21
```

The commit default is about three weeks of this repository's landing rate in
September 2026, and the age default is three runs of the weekly refresh below.
Each value must be an integer from 1 to 100,000 commits or 3,650 days
(`internal/config/devcontainer_policy.go`).

A missing configuration, one without a bootstrap, an unavailable bundle, a
source root that does not declare the Praetor module, a shallow clone, a drifted
bundle no commit records, and any git failure are errors, never a pass. The
check therefore needs the full history: CI checks out with `fetch-depth: 0`. It
measures a Praetor source checkout's own bundle; an adopted repository whose
bundle came from a separate Praetor checkout gets an error rather than a
measurement. Tests: `internal/devcontainer/bootstrap_freshness_test.go`,
`internal/config/devcontainer_policy_test.go` and
`cmd/standardsctl/devcontainer_freshness_test.go`.

`make devcontainer-freshness` runs the check inside `make verify-all`, and the
`DevContainer Bundle Freshness` step of `.github/workflows/ci.yml` runs it on
the light CI runs that skip verify-all. It is not part of `verify` on purpose:
`validateReadyBootstrap` in `internal/devcontainer/bootstrap.go` stays the
self-consistency check that freshness backstops, so verification needs no Git
history.

`.github/workflows/devcontainer-refresh.yml` keeps the bundle inside the bounds.
Every Monday at 05:00 UTC, and on dispatch, in the canonical repository only, it
records the freshness report, regenerates the bundle with
`praetorctl devcontainer generate --source-root . --force`, verifies it, and
force-pushes the result to `chore/devcontainer-bundle-refresh`. It opens one
pull request from that branch, or updates the open one, with the report in its
body. The commit is signed off under the `PRAETOR_BOT_NAME` and
`PRAETOR_BOT_EMAIL` identity `adopt.yml` uses. The job publishes with the
`PRAETOR_PR_TOKEN` secret when one is configured, because this repository does
not let `GITHUB_TOKEN` open pull requests and a pull request opened with it runs
no checks; without the secret the branch is pushed and the job fails naming it.
The pull request carries no receipt, so the landing pipeline takes it over like
a [Renovate pull request](contributing.md#renovate-pull-requests). To refresh by
hand, run the same generate command from a clean checkout and commit the
`.devcontainer` changes on their own.

## Migration

This release is breaking for existing adopter files and API callers.
`Verify` and `standardsctl audit` now report drift on a `devcontainer.json`
they previously accepted when it carries a key outside the managed schema, an
aliased or duplicated key, or content after the configuration object.
`LoadDevContainer` now refuses a key outside the managed schema. The
`Synthesize*` functions refuse more than `MaxLoopLimit` profiles or facets
instead of truncating them. The rendered features and `postCreateCommand` follow
the selected feature set, so an unedited non-bootstrap file can report drift
after the upgrade. To remediate, review the file and regenerate the bundle:

```bash
praetorctl devcontainer generate \
  --source-root /path/to/reviewed/praetor \
  --config .standards.yaml \
  --output .devcontainer/devcontainer.json \
  --force

praetorctl devcontainer verify
```

`--force` replaces only the named bundle files. Move hand-added keys such as
`initializeCommand` or `runArgs` out of the file first: audit cannot pass while
they remain.

Existing working custom or Praetor self-host DevContainers remain untouched.
Legacy generated files that refer to missing Praetor paths now fail verification.
Review those files, then run the generation command above with `--force` to
replace only the named bundle files. Do not use broad forced adoption solely to
repair a container. A new config-only invocation no longer produces an implied
runnable container: select a complete reviewed source checkout to activate the
bootstrap. API callers should use `PrepareBundle` and `WriteBundle`; the legacy
JSON writer cannot publish a recorded specification without its companions.

Runtime/profile selection is sourced from the selected pinned catalog entries.
Only the selected profile and facets contribute DevContainer features; duplicate
references must agree on options. Without a pinned catalog the profile still
decides: a `native-gpu-systems` repository receives the C/C++ toolchain
extensions and no Go feature, even when `framework` is declared alongside it.

The generated `postCreateCommand` follows the feature set the container actually
receives, not the profile list. `go run ./cmd/standardsctl compile-context` is
emitted only for a `framework` repository whose features install a Go toolchain:
the selected catalog decides that whenever one was selected, and the profile
decides it only on the legacy path with no selection. A `framework` repository
whose catalog carries no Go feature therefore starts with `make verify-all`
alone, which changes what `standardsctl audit` expects from an existing
non-bootstrap `.devcontainer/devcontainer.json` for that combination. Profiles
choose which IDE tooling is installed; they do not overrule the catalog about
what is present. More than `MaxLoopLimit` declared profiles or facets is refused
rather than truncated. This does not establish IDE feature-installation or
application-tool execution proof.

A `native-gpu-systems` DevContainer no longer passes clangd
`--compile-commands-dir=core/build`. Without that flag clangd searches each
edited file's ancestor directories and their `build/` subdirectories for
`compile_commands.json` ([clangd project setup](https://clangd.llvm.org/installation)),
which finds a root, `build/` or `core/build/` database alike; the fixed directory
pointed every other layout at a missing path. The DevContainer settings and the
`.vscode/settings.json` adoption writes share one definition,
`util.ClangdArguments` in `internal/util/clangd.go`; the regression tests are in
`internal/devcontainer/adopter_paths_test.go`. An unedited native bundle generated
before this change reports drift in `praetorctl devcontainer verify` and
`standardsctl audit`; regenerate it with the `--source-root ... --force` command
above. Adoption keeps an existing `.vscode/settings.json` unless it runs with
`--force`.

### Digest-only reviewed defaults

A ready bootstrap generated before #333 records the reviewed defaults as
`repository:tag@sha256:<digest>`. It still passes `praetorctl devcontainer
verify` and `standardsctl audit`, but `@devcontainers/cli` 0.89.0 cannot build
it. Regenerate it with the generation command above and `--force`: each image
is reported as `[RECORDED IMAGE REFRESHED]` and recorded digest-only, which
changes `Dockerfile.praetor` and the recorded `dockerfileSHA256`. The images
pulled are unchanged, because the digests are. To keep a tagged reference, pass
it with `--base-image` or `--builder-image`
(`TestDevContainerCLIDigestOnlyImages` in
`cmd/standardsctl/devcontainer_bootstrap_test.go`).

### Test sources leave the bootstrap archive

A ready bootstrap recorded before this change carries `_test.go` and `testdata`
files. Verification now refuses such an archive with `bootstrap source <path> is
test-only`, so `praetorctl devcontainer verify` and `standardsctl audit` report
it until the bundle is regenerated with the generation command above and
`--force`. Regeneration rewrites `devcontainer.json`, `Dockerfile.praetor` and
the `praetor-source.*.b64` parts; the recorded `sourceSHA256`, `archiveSHA256`
and `dockerfileSHA256` change even when no build source changed. The compiled
CLI does not change, because `go build` never read the removed files.

### The archive carries only the build closure

Before #501 the archive held every non-test Go file of the module, so packages
the CLI never imports filled the four-frame cap. Preparation now captures the
import closure of `cmd/standardsctl`, and the cap is eight frames. A ready
bootstrap recorded earlier still verifies: its extra packages pass the same
name rule, and its part count is within the new cap. Regenerating it with
`--force` shrinks the archive and changes the recorded digests. A bundle that
needs more than four parts is refused by a `praetorctl` released before this
change, so verify such a bundle with a current CLI. This command prints the
current usage of each bound:

```bash
go test -v -run TestRepositoryBootstrapSourceKeepsHeadroom ./internal/devcontainer
```

## Infrastructure test environments

The current GitOps profile selects tool features; it does not provision a
Kubernetes cluster, Ansible target, or VM. Keep editor tooling and task execution
requirements distinct. The [infrastructure environment plan](../plans/infrastructure-development-environments.md)
extends the matrix with configurable container, cluster and guest requirements,
backend admission and cleanup evidence. The [sandbox comparison](../research/infrastructure-sandboxes.md)
explains where Firecracker and other VM backends fit. These runtime adapters are
planned, and are not enabled by generating a DevContainer.
