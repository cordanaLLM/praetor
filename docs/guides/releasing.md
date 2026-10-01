# Releasing and Release Flavors

A commit on `main` becomes an installable version only when a `v*` tag points at it. This
page covers what that tag sets off, how to cut one, and how the moving flavor tags
(`bleeding`, `edge`, `latest`, `lts`) follow it.

## What a `v*` tag produces

| Consumer | Trigger | Result |
| :--- | :--- | :--- |
| `.github/workflows/release-binaries.yml` | `push` of a tag matching `v*` | GoReleaser (`.goreleaser.yaml`) builds `praetorctl`, `standardsctl`, `standards-mcp` and `standards-lsp` for Linux, macOS and Windows on amd64 and arm64, writes a CycloneDX and an SPDX SBOM per archive, signs `checksums.txt` keyless with cosign into `checksums.txt.sigstore.json`, and uploads everything into a **draft** release; it also builds the container image from the Linux `praetorctl` binaries, pushes it to `ghcr.io/cordanallm/praetor:<version>` and signs the pushed digest. The job then signs SLSA v1.0 provenance over every checksummed file, verifies both bundles and the image signature, pushes and signs the Helm chart as `oci://ghcr.io/cordanallm/charts/praetor:<version>`, attaches the provenance and publishes the release. `<version>` is the tag without its `v` |
| `go install github.com/cordanaLLM/praetor/cmd/standardsctl@latest` | any release version on the module proxy | `@latest` selects the highest release version; with no tag at all it falls back to a pseudo-version of `main` ([Go modules reference, version queries](https://go.dev/ref/mod#version-queries)). `praetorctl version` prints the installed tag, or the 12-character commit a pseudo-version ends with (`cmd/standardsctl/buildidentity.go`) |
| `.github/actions/praetor-adopt/action.yml` | `uses: cordanaLLM/praetor/.github/actions/praetor-adopt@<ref>` | the action builds `cmd/standardsctl` from its own checkout at that ref, so `@latest` runs the commit the moving `latest` tag points at; it never installs from the module proxy, and a local or copied action outside a praetor checkout fails instead ([docs/adoption.md](../adoption.md)) |
| `.github/workflows/sync-flavors.yml` | next run after the tag exists | moves `latest` to the highest `v*` tag |

Once the first release exists, `@latest` stops following `main`. An adopter that wants
unreleased commits has to ask for them, for example `@main`.

### Signing and SBOM toolchain

`release-binaries.yml` installs the tools the release assets depend on.
`internal/bump/scan_actions.go` records the same action pins as the baseline
`praetorctl bump` compares workflows against, and `internal/bump/release_pins_test.go`
fails when the workflows and that baseline disagree.

`praetorctl bump audit` compares each pin with that baseline by SemVer at the precision
of the less precise tag (`bump.ActionPinCurrent`): an exact `v4.1.2` is current against a
baseline `v4`, a moving `v4` is current against `v4.1.2`, and `v3.8.1` drifts behind
`v4.1.2`. A pin by full commit SHA with its release as a trailing comment
(`actions/checkout@<40-hex SHA> # v7.0.1`) is compared at that release exactly as a tag
pin of that release is, whatever spaces or tabs separate the comment from the SHA: the
one-space form Renovate and pinact write reads like the two-space form of the locked
documentation gate (`util.ParseSHAPin` in `internal/util/action_pin.go`,
`internal/bump/scan_actions_pinned_test.go`). A SHA pin without a release comment is
reported `[UNVERSIONED]`, neither drift nor up to date. The deprecation table is keyed
by major tag, and an exact release carries its major's deprecation, whether a tag
(`@v4.2.2`, `@4.2.2`) or a release comment (`# v4.2.2`) names it; an exact entry in
the table would override its major (`deprecatedRuntime`,
`TestScanWorkflowActionsExactReleaseCarriesMajorDeprecation`,
`TestScanWorkflowActionsNode24FirstMajorNotDeprecated`). Any other pin that is not a
version tag, such as a branch, is current only when it equals the baseline.
Commented-out `uses:` lines are not scanned. Tests:
`internal/bump/version_compare_test.go`.

`bump audit` then asks each SHA-pinned action's repository on github.com about the pin
(`bump.VerifyActionPins` in `internal/bump/scan_actions_verify.go`, through
`GitHubDriver.CommitAt` and `TagsAt` in `internal/forge/commit_lookup.go`):

| Row | The upstream says | Fails the report |
| :--- | :--- | :--- |
| drift or `[UP-TO-DATE]` | the release comment's tag, peeled, points at the pinned commit, or the commit carries a tag equal to the release at its precision (`# v4` after `v4` moved on, `# 7.0.0` for tag `v7.0.0`) | no |
| `[BAD-PIN]` | the commit does not exist (`action-pin-commit-missing`), or the comment's tag points at another commit or does not exist (`action-pin-release-mismatch`, naming the tags the commit carries) | yes, with file and line |
| `[UNVERSIONED]` | the commit exists; no release comment names it | no |
| `[UNVERIFIED]` | nothing: no token, offline, rate limited, or the repository is not visible | no; never up to date, and the reason is printed once under the rows |

The audit reads the token from `GITHUB_TOKEN`, `GH_TOKEN` or `gh auth token`, in that
order (`util.ResolveAuthTokenContext`); without one every SHA pin is `[UNVERIFIED]`. An
answer that says the forge cannot be asked stops the remaining questions, and one audit
asks at most 300. Tests: `internal/bump/scan_actions_verify_test.go`,
`internal/forge/commit_lookup_test.go`.

Every row other than `[UP-TO-DATE]` (`[DRIFT]`, `[DEPRECATED]`, `[BAD-PIN]`,
`[UNVERSIONED]` and `[UNVERIFIED]`) lowers the summary's `Up To Date` count and the
modernization score by one component; a deprecated or bad pin also carries a deprecation
and counts once (`internal/bump/audit_actions_test.go`). Drift, unversioned and unverified
rows alone leave the report passing. Any deprecation fails it, a bad pin, a missing probed
tool or an unexamined manifest included, and `bump audit` then exits non-zero after
printing the report (`TestRunBumpAuditExitsNonZeroOnFailedReport` in
`cmd/standardsctl/bump_audit_verdict_test.go`).

| Tool | Action pin | Installs | Why the pin reads the way it does |
| :--- | :--- | :--- | :--- |
| GoReleaser | `goreleaser/goreleaser-action@v7` | GoReleaser `~> v2`, from the step's `version` input | v7 moves the action runtime to node24 and adds only the optional `version-file` input, so the step's inputs are unchanged |
| Syft | `anchore/sbom-action/download-syft@v0.24.2` | Syft `v1.51.1` | The `.goreleaser.yaml` `sboms` args are unchanged, but a newer Syft catalogues more packages, so SBOM content differs from that of builds made with an older pin |
| cosign | `sigstore/cosign-installer@v4.1.2` | cosign `v3.0.6`, the installer's default | No `cosign-release` input: a version hold there is invisible to the `praetorctl bump` scanner, and `internal/forge/cosign_bundle_test.go` rejects one. The installer publishes no moving `v4` tag, so the pin is exact |
| Helm | `azure/setup-helm@v5` | Helm `v4.3.0`, from the step's `version` input | Pinned rather than the action's `latest` default, so a Helm release cannot change the packaged chart between two tags. `praetorctl bump` does not read the `version` input, so a newer Helm is picked up by hand |
| buildx | `docker/setup-buildx-action@v4` | A `docker-container` builder | GoReleaser's `dockers_v2` pushes a multi-platform manifest list, which needs that driver. The Dockerfile only copies prebuilt binaries, so no QEMU step is needed |
| GHCR login | `docker/login-action@v4` | Registry credentials for the job token | buildx and cosign push with the `packages: write` token through this login; `helm` logs in on its own with `helm registry login` |

### One flow, published last

Every asset is uploaded while the release is still a draft, and publishing is the job's
last step. A repository with GitHub immutable releases locks the release at publication,
so an upload after it fails; the flow never makes one (#43).

1. `helm lint deploy/helm/praetor` runs before anything is pushed.
2. `goreleaser release --clean` builds the archives, runs Syft over each archive (the
   `.goreleaser.yaml` `sboms` block), writes `checksums.txt` over the archives and SBOMs,
   signs it, and uploads all of it into a draft release (`release.draft: true`). Each
   archive carries the binaries, `README.md`, `CHANGELOG.md`, `LICENSE`, the `LICENSES/`
   texts and `THIRD-PARTY-NOTICES.md`. Its `dockers_v2` block builds the root `Dockerfile`
   from the Linux `praetorctl` binaries and the same license and notices files, which land
   under `/usr/local/share/praetor/` in the image, pushes a `linux/amd64` and `linux/arm64`
   manifest list with buildx's SBOM and provenance attestations, and `docker_signs` signs
   the pushed digest keyless.
3. `cosign verify-blob` checks `checksums.txt.sigstore.json` against this workflow's
   identity before anything else trusts it.
4. `praetorctl provenance -checksums dist/checksums.txt` writes one in-toto SLSA v1.0
   statement whose subjects are every line of `checksums.txt`, with the workflow identity
   as the builder. Each subject digest is recomputed from the file in `dist/` and must
   match its line (`internal/supplychain/slsa.go`), and each file's content is checked
   against its name ([content checks](#content-checks)).
5. `cosign attest-blob --statement` signs that statement into
   `provenance.intoto.json.sigstore.json`, and `cosign verify-blob-attestation` checks the
   bundle against every file `checksums.txt` names.
6. `cosign verify` checks the image signature on the digest GoReleaser recorded in
   `dist/artifacts.json`, never on the tag. `docker buildx imagetools inspect` then reads
   the SBOM and provenance attestations of that digest and fails the job when a platform
   that `dist/artifacts.json` lists lacks either one; a builder without attestation support
   would otherwise drop both without an error.
7. `helm package --version <version> --app-version <version>` packages the chart,
   `helm push` pushes it to `oci://ghcr.io/cordanallm/charts`, and cosign signs and then
   verifies the digest `helm push` reports. The chart's default image tag is its
   `appVersion`, so the published chart pulls the image from step 2.
8. `gh release upload` attaches the statement and its bundle, and
   `gh release edit --draft=false` publishes.

`internal/forge/release_flow_test.go` fails `go test` when the release stops being a draft,
publishing moves ahead of signing or verification, an upload follows publication, a step
catalogues the checkout with `syft dir:.` again, or publication moves ahead of the image
signature, image attestation and chart verification. `internal/forge/workflow_guard_test.go`
fails when the image name in `.goreleaser.yaml`, `deploy/helm/praetor/values.yaml` or the
job's `IMAGE` and `CHART_REPOSITORY` stops being the lowercased `.standards.yaml` identity;
GHCR accepts lowercase names only. `internal/supplychain/notices_test.go` fails when an
archive, `extra_files` or the `Dockerfile` drops one of the license and notices files, and
when `THIRD-PARTY-NOTICES.md` falls out of step with `go.mod`, the embedded npm lock, the
embedded figure engine (the interfig pin and the packages the committed player bundles) or the
image's base. After a dependency bump, `praetorctl sbom notices` (`make third-party-notices`)
regenerates the notices' component tables; a new component or an unreviewed license stops it
until its row is written by hand (`internal/supplychain/notices.go`). ADR-0013 records the
design.

A failure in steps 3 to 7 leaves the release a draft, but the image, and after step 7 the
chart, are already on GHCR. A rerun of the job pushes over the same tags.

### First release: make the GHCR packages public

GHCR creates each package private on its first push, so an anonymous `docker pull` or
`helm install` fails until an organisation owner changes the visibility of the `praetor` and
`charts/praetor` packages to public, once, in the organisation's package settings
([GitHub docs, configuring a package's visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#configuring-visibility-of-packages-for-an-organization)).
Later pushes keep the visibility.

### Building the image locally

The image's build context holds the binaries GoReleaser compiled, one
`<os>/<arch>/praetorctl` per platform, plus the license and notices files `extra_files`
lists, so `docker build .` in a checkout has no binary to copy.
A snapshot build compiles the binaries and builds one image per platform without pushing:

```bash
GORELEASER_CURRENT_TAG=v0.0.0 goreleaser release --snapshot --clean --skip=sign,sbom
docker run --rm ghcr.io/cordanallm/praetor:0.0.1-snapshot-amd64 version
```

`--snapshot` skips publishing but not signing, and keyless signing needs the workflow's OIDC
token, so `--skip=sign` is required outside CI; `--skip=sbom` drops the Syft dependency.
`GORELEASER_CURRENT_TAG` is needed while the newest tag is a moving flavor tag such as
`bleeding`, which is not SemVer and fails the snapshot version template. The run leaves
`<version>-snapshot-amd64` and `<version>-snapshot-arm64` images.

Build Level 3 is not claimed. The provenance is generated and signed in the same job as
the build steps, and SLSA Build Level 3 requires signing that the build steps cannot reach.
Measuring the declared `supply_chain.slsa_level` against the workflow is tracked in #330.

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
SIGNER="https://github.com/cordanaLLM/praetor/.github/workflows/release-binaries.yml@refs/tags/$TAG"
gh release download "$TAG" --repo cordanaLLM/praetor

cosign verify-blob \
  --certificate-identity "$SIGNER" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt

cosign verify-blob-attestation \
  --certificate-identity "$SIGNER" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --type slsaprovenance1 \
  --bundle provenance.intoto.json.sigstore.json \
  standards_${TAG#v}_linux_amd64.tar.gz
```

`checksums.txt` covers every archive and every per-archive SBOM, so one bundle
verification plus the checksum check covers the whole set. The provenance names the same
files as subjects, so `verify-blob-attestation` accepts any one of them.

| Signed file | Bundle | Signed by |
| :--- | :--- | :--- |
| `checksums.txt` | `checksums.txt.sigstore.json` | `.github/workflows/release-binaries.yml` (via `.goreleaser.yaml` `signs.cosign-keyless`) |
| `provenance.intoto.json` | `provenance.intoto.json.sigstore.json` | `.github/workflows/release-binaries.yml` (`cosign attest-blob`) |

Each archive's SBOMs, `<archive>.cyclonedx.json` and `<archive>.spdx.json`, carry no bundle
of their own: `checksums.txt` lists them, so the checksums bundle covers them.

`internal/forge/cosign_bundle_test.go` replays the flag shape against the real workflow and
GoReleaser files, so a return to the removed v2 flags fails `go test` rather than a tag push.

## SLSA provenance statements

`praetorctl provenance` writes an in-toto v1 statement with an SLSA v1.0 provenance
predicate for one artifact file, or for every file a sha256sum manifest lists. The
statement it writes is **unsigned**: it becomes an attestation only once a signer wraps it
in a DSSE envelope and a verifier checks that envelope's signature and signer identity,
which the release flow above does with `cosign attest-blob`.

```bash
praetorctl provenance --file dist/praetorctl_linux_amd64.tar.gz --out provenance.json
praetorctl provenance --checksums dist/checksums.txt --out provenance.json
praetorctl provenance --checksums dist/checksums.txt --uki 'boot/vmlinuz-*.efi' --out provenance.json
```

| Flag | Default | Effect |
| :--- | :--- | :--- |
| `--file` | required unless `--checksums` | Artifact whose bytes are streamed through SHA-256; the result is the subject digest |
| `--checksums` | none | sha256sum manifest such as GoReleaser's `checksums.txt`; every listed file, resolved beside the manifest, becomes a subject digested from its bytes, and each line's digest is a cross-check like `--digest` |
| `--artifact` | base name of `--file` | Subject name |
| `--digest` | none | Expected SHA-256 (64 lowercase hex characters); the run fails unless `--file` hashes to it |
| `--builder` | `$GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF` in GitHub Actions; required elsewhere | Builder ID recorded in the predicate |
| `--content-check` | `enforce` | What a file whose bytes are not the type its name declares does: `enforce` refuses the statement, `report` attests the file and prints an `UNVERIFIED` warning ([content checks](#content-checks)) |
| `--uki` | none | Glob declaring every subject whose name it matches a Unified Kernel Image, which then needs a `.linux` section whatever its name; repeatable, and each glob must match a subject ([declaring a UKI](#declaring-a-uki)) |
| `--out` | stdout | Output file |

The subject digest always comes from the file's bytes, never from `--digest`, so a
statement cannot name a digest nobody computed. The run is refused when:

- `--file` is missing, including when `--digest` is given alone, unless `--checksums`
  names the subjects; `--checksums` beside `--file`, `--artifact` or `--digest`;
- `--builder` is missing outside GitHub Actions, or blank;
- a `--checksums` line is not `<sha256>  <name>` or `<sha256> *<name>`, names a path
  outside the manifest's directory, or lists a file that hashes to anything else;
- the file, or any file the manifest lists, is empty, larger than 2 GiB
  (`supplychain.MaxArtifactBytes`), a symlink, not a regular file, or changes while it is
  read;
- `--digest` is malformed or differs from the computed digest. The error names the
  computed digest;
- under `--content-check=enforce`, a file's bytes are not the type its name declares, or
  `--content-check` is neither `enforce` nor `report`;
- a `--uki` glob is empty, malformed, or matches no subject name.

### Content checks

A checksum and a signature prove that a file is the one that was built, not that the build
produced what the file's name promises: a text file named `.deb` or random bytes named `.efi`
pass both. Before a file becomes a subject, `praetorctl provenance` therefore checks its bytes
against the type its subject name declares. The check reads the same bytes the digest streams
(`contextopt.DigestBinarySnapshotTo`), so what was checked is what was attested.

| Subject name | Required content |
| :--- | :--- |
| `*.deb`, `*.udeb`, `*.ddeb` | An `ar` archive ([deb(5)](https://man7.org/linux/man-pages/man5/deb.5.html)) whose first member is `debian-binary` holding a `2.x` format version. After it, skipping members whose names start with `_`, the next member is `control.tar` or `control.tar.<ext>`, and after that, again skipping `_` members, `data.tar` or `data.tar.<ext>`. Members after `data.tar` are accepted whatever their names. No member header or member data may be cut off |
| `*.efi` | A complete PE/COFF image (DOS header, PE signature, COFF and optional headers, section table, every section's raw data inside the file) whose subsystem is an EFI application, driver or ROM (10 to 13) |
| `*.efi` presented as a UKI, or any subject declared one with `--uki` | The same, plus a non-empty `.linux` section, the one section the [UAPI Unified Kernel Image specification](https://uapi-group.org/specifications/specs/unified_kernel_image/) requires. A bare EFI-stub kernel has none |

An `.efi` name is presented as a Unified Kernel Image in two cases. The first is a file
directly in an `EFI/Linux/` directory, where the
[Boot Loader Specification](https://uapi-group.org/specifications/specs/boot_loader_specification/)
puts Type #2 images. The second is a base name with the word `uki`, a word being a run of
letters and digits (`image.uki.efi`, `arch-linux-uki.efi`, but not `ukify.efi`). Kernel words
do not count: `vmlinuz.efi`, `vmlinuz-7.2.4.efi` and `vmlinuz-linux.efi` only have to be EFI
images, because upstream Linux builds a bare EFI-stub kernel as `vmlinuz.efi` under
`CONFIG_EFI_ZBOOT` and EFISTUB setups copy the kernel to such names, neither with a `.linux`
section (#616).

A systemd-stub addon (`*.addon.efi`) is never presented as a UKI: it carries `.cmdline`,
`.dtb`, `.initrd` or `.ucode` sections and no `.linux`, and
[systemd-stub(7)](https://man7.org/linux/man-pages/man7/systemd-stub.7.html) loads it from
`foo.efi.extra.d/` inside `EFI/Linux/` or from `loader/addons/`. So
`EFI/Linux/foo.efi.extra.d/quiet.addon.efi` and `uki-cmdline.addon.efi` only have to be EFI
images, as do `BOOTX64.EFI` and `systemd-bootx64.efi`. The extension, the suffix and the
word are compared without regard to case.

#### Declaring a UKI

A UKI whose name matches neither case, for example `vmlinuz-7.2.4.efi` built by `ukify`, is
declared with `--uki`. Each subject whose name matches a `--uki` glob gets the UKI rule
whatever its name or extension, so a placeholder or a bare kernel under that name is refused.
The flag is repeatable and works with `--file` (matched against `--artifact` or the file's
base name) and with `--checksums` (matched against each manifest name).

A glob is slash-separated and matched against the whole subject name, case included, after
backslashes in the name become slashes. Each segment is a `path.Match` pattern for one name
segment, so `*` and `?` never cross a `/`, and a segment that is exactly `**` spans any number
of segments: `vmlinuz-*.efi` matches `vmlinuz-7.2.4.efi` but not `boot/vmlinuz-7.2.4.efi`,
which `**/vmlinuz-*.efi` matches. A glob that is empty or malformed is refused before any file
is read. A glob that matches no subject refuses the statement, so a mistyped declaration does
not leave a UKI unchecked (`internal/supplychain/content_uki.go`, `content_uki_test.go`).

With the default `--content-check=enforce`, one mismatching file refuses the whole statement,
and the error names the file, the expected format and what was found instead, for example
`the EFI image has no .linux section`. When the subject name is not the file's base name, or
a `--uki` glob declared the subject, the error names that too, since it chose the rule. `--content-check=report` attests the file anyway and
prints `warning: content of <name> is UNVERIFIED: ...` on stderr; use it only while a broken
build is being repaired. A file type without a rule is attested as before and named in one
`note: content unchecked for N subject(s) ...` line on stderr, never counted as verified.
Praetor's own release archives (`.tar.gz`, `.zip`) and SBOMs (`.json`) are in that group.

`internal/supplychain/content_test.go` covers each rule with valid layouts, mismatches and
truncated files fed in chunk sizes that split every header;
`internal/supplychain/content_tools_test.go` attests a package `dpkg-deb` builds, a UKI
`ukify` builds (declared with a UKI glob) and an addon `ukify` builds under both addon
locations. It also attests the installed kernel as `vmlinuz-bare.efi` and refuses it once a UKI
glob declares it. Each test runs where its tools are installed: `dpkg-deb`; `ukify` with
systemd's stub for the running architecture; for the kernel test, a kernel under
`/usr/lib/modules` that `debug/pe` reads as an EFI-subsystem PE image. Elsewhere it is skipped
with the reason.

Every statement records the `buildDefinition.buildType`
`https://cordanallm.github.io/praetor/slsa/build/v1` (`supplychain.DefaultBuildType`). Every
run prints an `UNSIGNED` warning on stderr, so stdout stays parseable JSON, and the
`--out` summary line calls the statement unsigned. The statement itself holds exactly the
four in-toto v1 [Statement](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
fields, `_type`, `subject`, `predicateType` and `predicate`, and each subject only `name`
and `digest`. It carries no unsigned marker of its own, because the in-toto v1 parsing
rules do not bind every verifier: sigstore-go, which `cosign verify-blob-attestation` uses,
decodes the signed payload with `protojson` and fails with `unable to extract statement from
envelope` on any field the Statement does not define. `supplychain.CheckInTotoStatement`
(`internal/supplychain/intoto.go`) checks that shape before the command writes anything
and refuses a statement that does not fit it. `internal/supplychain/slsa_test.go`,
`internal/supplychain/intoto_test.go`, `internal/supplychain/provenance_subjects_test.go`
and `cmd/standardsctl/provenance_cli_test.go` cover these cases.

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
config load.

`stability` is a free-form label. `plan` and `sync` print it on every flavor line, any value
loads, and it never moves, holds or protects a tag: two flavors that differ only in `stability`
get the same plan. Caution for a pre-release channel comes from `update_frequency: manual`,
which holds the tag until named, and from the source ref: a `refs/tags/v*` source never
resolves to a prerelease tag. `TestPlanSelected_Negative_StabilityNeverChangesTheAction` in
[`internal/flavors/stability_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/flavors/stability_test.go)
pins this.

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

A `refs/tags/v*` source reads at most 4096 matching tags (`maxSemverTagCandidates` in
`cmd/standardsctl/flavors.go`). When the pattern matches more, `plan` and `sync` exit
non-zero with `matches more than 4096 tags` before printing a plan or moving any tag,
because the highest stable tag can sort after the first 4096 and pending would claim no
stable tag exists (#389). A `sync-flavors.yml` run then fails and moves no flavor, not
even `bleeding` on `refs/heads/main`, until `source_ref` is narrowed, for example to
`refs/tags/v2.*`.

A git read failure while resolving a source ref or current tag (such as a timeout, an
unreadable `packed-refs` file, dubious ownership, a permission error, or a cancelled
context) is likewise an error naming the ref or pattern and the cause (#671), and `plan`
and `sync` exit non-zero without moving any tag; only a successful read that matches
nothing, or only prereleases, stays pending. git itself answers "no such commit" for a
loose ref with unreadable contents or one naming a missing object, so such a ref still
reads as pending.

Moving tags are lightweight tags created with `git tag --no-sign`. A workstation with
`tag.gpgSign=true` would otherwise turn them into signed annotated tags that need a
message, and the sync would fail.

`cmd/standardsctl/flavors_cli_test.go` covers pending flavors, `--strict`, failed git reads,
the atomic push against a bare repository, and the signing configuration.
`cmd/standardsctl/flavors_overflow_test.go` covers the 4096-tag bound and the overflow error.
`cmd/standardsctl/flavors_frequency_test.go` and `internal/flavors/frequency_test.go` cover
held manual flavors, `--flavor`, and the refused frequencies.

## Known limits

- `latest` ignores tags that do not parse as SemVer even though they match the `v*` glob
  (for example a stray `v-nightly`); those are treated as absent, not as an error.
- `lts` matches local branches only. A CI checkout creates a local branch for `main` alone,
  so an `lts-*` branch that exists only on the remote leaves `lts` pending.
