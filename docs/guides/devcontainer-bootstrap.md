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

Runtime/profile selection remains a separate open limitation: the existing
feature generator still inserts Go (BUG-651). This correction does not implement
C# tooling, a language-aware feature resolver, or IDE feature-installation proof.
