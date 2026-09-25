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
tracked and nonignored untracked Go sources, tests, `go.mod`, `go.sum`, and
`LICENSE` are captured twice; a changed snapshot fails preparation. Embedded
assets and unsupported native build inputs fail explicitly rather than being
omitted. Capture is bounded to 4,096 files, 8 MiB total, and 1 MiB per file.

The recorded `customizations.praetor.bootstrap` specification identifies the
source snapshot, compressed archive, Dockerfile, and immutable builder/base
images. The compiled CLI retains the version declared by the selected source;
its displayed version is not the bundle digest. Read the source identity from
the retained bootstrap specification and verified companions. The compressed
source is carried in at most four 512 KiB base64 files,
alongside `Dockerfile.praetor`. Keep these files together with the JSON. They are
source-bearing artifacts: select and review the source before copying a bundle
to another repository. The hashes establish integrity, not source authenticity
or a release signature.

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

Verification compares the file itself against the render of the expected
configuration, after normalising CRLF line endings to LF so a Windows checkout
with `core.autocrlf=true` is not reported as drift (HISS-21). JSON forbids an
unescaped carriage return inside a string, so every CRLF in the file is
whitespace between tokens and the normalisation cannot hide an edit.
Verification does not compare a re-render of what decoded, because
Go's JSON decoder matches member names case-insensitively and keeps the last of a
duplicate pair: `POSTCREATECOMMAND`, `RemoteUser` and a repeated
`postCreateCommand` all decode into the managed struct and re-marshal to the
spec spelling, while the DevContainer runtime reads object keys case-sensitively
and would run none of them. A key outside the managed schema, such as a
hand-added `initializeCommand` or `runArgs`, is named in the rejection; every
other edit, including whitespace and content after the configuration object, is
reported as drift. Keep such additions in a custom DevContainer, which adoption
preserves and reports as execution-unverified.

Without `--source-root`, or with an explicitly selected config-only catalog,
generation writes an `unavailable` configuration and returns an error. Its
startup fails with an actionable message; it cannot report a missing CLI as
ready. Invalid explicit source paths, mutable image references, incomplete
bundles, symlinks, and altered companions are errors. Optional `--builder-image`
and `--base-image` overrides must include lowercase SHA-256 digests.

Adoption uses its explicit `--lock-source-root` as the bootstrap source and the
same planned or preserved manifest that audit consumes. A config-only catalog
can supply governance policy while bootstrap remains visibly unavailable.
Dry-run lists planned companion paths without writing them. Existing custom
DevContainers are preserved and reported as execution-unverified.

## Migration

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

## Infrastructure test environments

The current GitOps profile selects tool features; it does not provision a
Kubernetes cluster, Ansible target, or VM. Keep editor tooling and task execution
requirements distinct. The [infrastructure environment plan](../plans/infrastructure-development-environments.md)
extends the matrix with configurable container, cluster and guest requirements,
backend admission and cleanup evidence. The [sandbox comparison](../research/infrastructure-sandboxes.md)
explains where Firecracker and other VM backends fit. These runtime adapters are
planned, and are not enabled by generating a DevContainer.
