# Releasing and Release Flavors

A commit on `main` becomes an installable version only when a `v*` tag points at it. This
page covers what that tag sets off, how to cut one, and how the moving flavor tags
(`bleeding`, `edge`, `latest`, `lts`) follow it.

## What a `v*` tag produces

| Consumer | Trigger | Result |
| :--- | :--- | :--- |
| `.github/workflows/release-binaries.yml` | `push` of a tag matching `v*` | GoReleaser (`.goreleaser.yaml`) builds `praetorctl`, `standardsctl`, `standards-mcp` and `standards-lsp` for Linux, macOS and Windows on amd64 and arm64, attaches Syft SBOMs, signs `checksums.txt` keyless with cosign into `checksums.txt.sigstore.json`, and publishes a GitHub release |
| `go install github.com/cordanaLLM/praetor/cmd/standardsctl@latest` | any release version on the module proxy | `@latest` selects the highest release version; with no tag at all it falls back to a pseudo-version of `main` ([Go modules reference, version queries](https://go.dev/ref/mod#version-queries)) |
| `.github/actions/praetor-adopt/action.yml` | `uses: cordanaLLM/praetor/.github/actions/praetor-adopt@<ref>` | the action builds `cmd/standardsctl` from its own checkout at that ref, so `@latest` runs the commit the moving `latest` tag points at; it never installs from the module proxy, and a local or copied action outside a praetor checkout fails instead ([docs/adoption.md](../adoption.md)) |
| `.github/workflows/sync-flavors.yml` | next run after the tag exists | moves `latest` to the highest `v*` tag |

Once the first release exists, `@latest` stops following `main`. An adopter that wants
unreleased commits has to ask for them, for example `@main`.

### Signing and SBOM toolchain

`release-binaries.yml` and `sbom.yml` install the tools the release assets depend on.
`internal/bump/scan_actions.go` records the same action pins as the baseline
`praetorctl bump` compares workflows against, and `internal/bump/release_pins_test.go`
fails when the workflows and that baseline disagree.

| Tool | Action pin | Installs | Why the pin reads the way it does |
| :--- | :--- | :--- | :--- |
| GoReleaser | `goreleaser/goreleaser-action@v7` | GoReleaser `~> v2`, from the step's `version` input | v7 moves the action runtime to node24 and adds only the optional `version-file` input, so the step's inputs are unchanged |
| Syft | `anchore/sbom-action/download-syft@v0.24.2` | Syft `v1.51.1` | The invocations `syft dir:. -o cyclonedx-json=…` and the `.goreleaser.yaml` `sboms` args are unchanged, but a newer Syft catalogues more packages, so SBOM content differs from that of builds made with an older pin |
| cosign | `sigstore/cosign-installer@v4.1.2` | cosign `v3.0.6`, the installer's default | No `cosign-release` input: a version hold there is invisible to the `praetorctl bump` scanner, and `internal/forge/cosign_bundle_test.go` rejects one. The installer publishes no moving `v4` tag, so the pin is exact |

## Verifying a published release

Every signature this repository publishes is a **Sigstore bundle**: one `.sigstore.json`
file that carries the signature, the short-lived Fulcio certificate and the Rekor
transparency-log entry together. The signing steps used to be written for cosign v2: each
SBOM would have carried a detached `.sig` plus `.cert`, and `checksums.txt` only a
`checksums.txt.sig`, because the `.goreleaser.yaml` signs block set no `certificate` name.
No `v*` release was published in that shape, so every release carries bundles only, and
`cosign verify-blob --signature ... --certificate ...` does not apply to any of them.
`cosign sign-blob` in cosign v3 has no `--output-signature` or `--output-certificate` flag
at all ([`cosign_sign-blob.md`](https://github.com/sigstore/cosign/blob/v3.1.3/doc/cosign_sign-blob.md)).

Verification needs cosign v3 or newer (CI installs cosign `v3.0.6`, the default of
`sigstore/cosign-installer@v4.1.2`) and the tag you are checking:

```bash
TAG=v0.1.0
gh release download "$TAG" --repo cordanaLLM/praetor

cosign verify-blob \
  --certificate-identity "https://github.com/cordanaLLM/praetor/.github/workflows/release-binaries.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt
```

`checksums.txt` covers every archive, so one bundle verification plus the checksum check
covers the whole binary set.

| Signed file | Bundle | Signed by |
| :--- | :--- | :--- |
| `checksums.txt` | `checksums.txt.sigstore.json` | `.github/workflows/release-binaries.yml` (via `.goreleaser.yaml` `signs.cosign-keyless`) |
| `cordana-standards-cyclonedx.json` | `cordana-standards-cyclonedx.json.sigstore.json` | `.github/workflows/release-binaries.yml` |
| `cordana-standards-spdx.json` | `cordana-standards-spdx.json.sigstore.json` | `.github/workflows/release-binaries.yml` |
| `praetor-cyclonedx.json` | `praetor-cyclonedx.json.sigstore.json` | `.github/workflows/sbom.yml` |
| `praetor-spdx.json` | `praetor-spdx.json.sigstore.json` | `.github/workflows/sbom.yml` |

`--certificate-identity` names the workflow file that signed the artifact, so an SBOM from
`sbom.yml` is verified with `.../sbom.yml@refs/tags/$TAG` rather than the
`release-binaries.yml` identity above.

`internal/forge/cosign_bundle_test.go` replays the flag shape against the real workflow and
GoReleaser files, so a return to the removed v2 flags fails `go test` rather than a tag push.

## SLSA provenance statements

`praetorctl provenance` writes an in-toto v1 statement with an SLSA v1.0 provenance
predicate for one artifact file. No release workflow calls it yet, and the statement it
writes is **unsigned**: it becomes an attestation only once a signer wraps it in a DSSE
envelope and a verifier checks that envelope's signature and signer identity.

```bash
praetorctl provenance --file dist/praetorctl_linux_amd64.tar.gz --out provenance.json
```

| Flag | Default | Effect |
| :--- | :--- | :--- |
| `--file` | required | Artifact whose bytes are streamed through SHA-256; the result is the subject digest |
| `--artifact` | base name of `--file` | Subject name |
| `--digest` | none | Expected SHA-256 (64 lowercase hex characters); the run fails unless `--file` hashes to it |
| `--builder` | `ghcr.io/cordanallm/builder` | Builder ID recorded in the predicate |
| `--out` | stdout | Output file |

The subject digest always comes from the file's bytes, never from `--digest`, so a
statement cannot name a digest nobody computed. The run is refused when:

- `--file` is missing, including when `--digest` is given alone;
- the file is empty, larger than 2 GiB (`supplychain.MaxArtifactBytes`), a symlink, not a
  regular file, or changes while it is read;
- `--digest` is malformed or differs from the computed digest. The error names the
  computed digest.

Every statement carries a `praetorEmission` extension field with `"signed": false`, and
every run prints an `UNSIGNED` warning on stderr, so stdout stays parseable JSON. The
in-toto v1 [parsing rules](https://github.com/in-toto/attestation/blob/main/spec/v1/README.md#parsing-rules)
tell consumers to ignore fields they do not recognize, so the marker changes nothing for
a verifier. `internal/supplychain/slsa_test.go` and
`cmd/standardsctl/provenance_cli_test.go` cover these cases.

## Cutting a release

The pull-request flow squash-merges into `main`, so the commit prepared on a branch is not
the commit that lands. The tag is therefore made after the merge, on `main`, and
`praetorctl release` deliberately does not tag.

1. Prepare the changelog on a branch cut from `origin/main`:

   ```bash
   git switch -c release/v0.1.0 origin/main
   praetorctl release --version=v0.1.0 --date=2026-09-18
   ```

   `praetorctl release` (`internal/release/release.go`) checks the version is SemVer and
   the tree is clean, runs `make verify-all`, then renders `changelog.d/` into a new
   `CHANGELOG.md` section and removes the rendered fragments. With no fragment to render,
   because `changelog.d/` is missing or empty, it fails with `no changelog fragments to
   render` and leaves `CHANGELOG.md` untouched. Commit the result and merge it through a
   pull request.

2. Tag the merged commit and push only the tag:

   ```bash
   git fetch origin
   git tag -a v0.1.0 -m "v0.1.0" origin/main
   git push origin v0.1.0
   ```

   Use `git tag -s` instead of `-a` to sign the tag.

3. Check the result:

   ```bash
   gh run list --workflow release-binaries.yml --limit 1
   gh release view v0.1.0
   praetorctl flavors plan   # latest now resolves to v0.1.0
   ```

   The flavor sync runs on the next push to `main`, every six hours, or on
   `gh workflow run sync-flavors.yml`.

`internal/changelog/tracked_fragments_test.go` renders the tracked fragments into a
temporary copy on every `go test`, so a fragment that would stop step 1 fails the change
that adds it.

## Release flavors

`.config/flavors.yaml` declares each flavor and the ref it follows:

| Flavor | Source ref | Update frequency | Stability |
| :--- | :--- | :--- | :--- |
| `bleeding` | `refs/heads/main` | `on_push` | experimental |
| `edge` | `refs/heads/main` | `manual` | pre-release |
| `latest` | `refs/tags/v*`, highest SemVer tag with no prerelease component | `on_release` | stable |
| `lts` | `refs/heads/lts-*`, highest by the same sort | `on_patch` | enterprise-stable |

`update_frequency` decides whether a sync moves the tag on its own. `on_push`, `on_release`
and `on_patch` name the event that moves the source ref; every sync follows it
automatically, and an undeclared frequency behaves the same way. `manual` holds the tag:
`plan` and `sync` report the flavor as `HELD` and leave its tag where it is, whatever its
source resolves to, until an operator names it with `--flavor`. Any other value fails the
config load. `stability` is reported on every plan line and changes nothing else.

```bash
praetorctl flavors plan                 # print each flavor's current and target commit
praetorctl flavors sync                 # move the local tags
praetorctl flavors sync --push          # ...and publish them
praetorctl flavors sync --strict        # fail if any source ref resolves to nothing
praetorctl flavors sync --flavor=edge   # move a manual flavor, and only the flavors named
```

| Flag | Default | Effect |
| :--- | :--- | :--- |
| `--push` | off | Publishes every tag the sync moved in one `git push --atomic --force`, so the remote takes all of them or none |
| `--remote` | `origin` | Remote that `--push` publishes to |
| `--strict` | off | Fails before any tag moves when a declared source ref resolves to no commit |
| `--flavor` | all declared | Comma-separated flavors to plan or sync; the only way a `manual` flavor moves. An undeclared name fails before any tag moves |
| `--config` | `.config/flavors.yaml` | Flavor declarations |
| `--dir` | `.` | Repository root |

A flavor whose source ref resolves to no commit is **pending**. That is the normal state for
`latest` before the first `v*` tag, for `latest` while only prerelease tags exist (a
`v0.2.0-rc.1` alone never stands in for `latest`, see #245), and for `lts` before the first
`lts-*` branch. `sync` prints it as `Pending <flavor>`, leaves its tag exactly where it is,
and still moves every other flavor. It never falls back to `HEAD`, so a stable channel is
never aliased to `main`. The workflow runs `flavors sync --push`, so the tags published are
the flavors declared in the config, not a list of names kept in YAML. A scheduled or push run
holds `edge`; dispatching `.github/workflows/sync-flavors.yml` with the `flavor` input (for
example `edge`) passes `--flavor` and moves only the flavors named.

Moving tags are lightweight tags created with `git tag --no-sign`. A workstation with
`tag.gpgSign=true` would otherwise turn them into signed annotated tags that need a
message, and the sync would fail.

`cmd/standardsctl/flavors_cli_test.go` covers pending flavors, `--strict`, the atomic push
against a bare repository, and the signing configuration.
`cmd/standardsctl/flavors_frequency_test.go` and `internal/flavors/frequency_test.go` cover
held manual flavors, `--flavor`, and the refused frequencies.

## Known limits

- `latest` ignores tags that do not parse as SemVer even though they match the `v*` glob
  (for example a stray `v-nightly`); those are treated as absent, not as an error.
- `lts` matches local branches only. A CI checkout creates a local branch for `main` alone,
  so an `lts-*` branch that exists only on the remote leaves `lts` pending.
