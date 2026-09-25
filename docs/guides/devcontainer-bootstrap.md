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
The selected source must be a Git checkout declaring the Praetor module. Its
tracked and nonignored untracked Go build sources, `go.mod`, `go.sum`, and
`LICENSE` are captured twice; a changed snapshot fails preparation. Go's test
surface, `_test.go` files and anything under a `testdata` directory, is not
captured: the generated Dockerfile only runs `go build ./cmd/standardsctl`,
which reads neither. The Git pathspec and the name check apply one rule,
`util.IsGoNonTestSource` (`internal/util/gosource.go`), so a test file cannot
enter a captured set, and an archive carrying one fails verification
(`internal/devcontainer/bootstrap_source.go`). Embedded assets and unsupported
native build inputs fail explicitly rather than being omitted. Capture is
bounded to 4,096 files, 8 MiB total, and 1 MiB per file.

The recorded `customizations.praetor.bootstrap` specification identifies the
source snapshot, compressed archive, Dockerfile, and immutable builder/base
images. The compiled CLI retains the version declared by the selected source;
its displayed version is not the bundle digest. Read the source identity from
the retained bootstrap specification and verified companions. The compressed
source is carried in at most four 512 KiB base64 files, alongside
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
repository's `.gitattributes`, as Praetor does for itself (`.gitattributes:47`).
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
and `--base-image` overrides must include lowercase SHA-256 digests.

Adoption uses its explicit `--lock-source-root` as the bootstrap source and the
same planned or preserved manifest that audit consumes. A config-only catalog
can supply governance policy while bootstrap remains visibly unavailable.
Dry-run lists planned companion paths without writing them. Existing custom
DevContainers are preserved and reported as execution-unverified.

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

### Test sources leave the bootstrap archive

A ready bootstrap recorded before this change carries `_test.go` and `testdata`
files. Verification now refuses such an archive with `bootstrap source <path> is
test-only`, so `praetorctl devcontainer verify` and `standardsctl audit` report
it until the bundle is regenerated with the generation command above and
`--force`. Regeneration rewrites `devcontainer.json`, `Dockerfile.praetor` and
the `praetor-source.*.b64` parts; the recorded `sourceSHA256`, `archiveSHA256`
and `dockerfileSHA256` change even when no build source changed. The compiled
CLI does not change, because `go build` never read the removed files.

## Infrastructure test environments

The current GitOps profile selects tool features; it does not provision a
Kubernetes cluster, Ansible target, or VM. Keep editor tooling and task execution
requirements distinct. The [infrastructure environment plan](../plans/infrastructure-development-environments.md)
extends the matrix with configurable container, cluster and guest requirements,
backend admission and cleanup evidence. The [sandbox comparison](../research/infrastructure-sandboxes.md)
explains where Firecracker and other VM backends fit. These runtime adapters are
planned, and are not enabled by generating a DevContainer.
