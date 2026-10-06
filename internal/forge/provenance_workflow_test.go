package forge

import (
	"cmp"
	"context"
	"strings"
	"testing"
)

// releaseWorkflow is the path every single-workflow case writes.
const releaseWorkflow = ".github/workflows/release.yml"

// reusableJob wraps one job-level uses: into a workflow document a tag push starts.
func reusableJob(uses string) string {
	return "on:\n  push:\n    tags: ['v*']\njobs:\n  provenance:\n    uses: " + uses + "\n"
}

// calledWorkflow wraps steps into a reusable workflow document (on: workflow_call).
func calledWorkflow(steps string) string {
	return "on:\n  workflow_call:\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" + steps
}

// measure runs MeasureProvenance over a repository holding files.
func measure(t *testing.T, files map[string]string) ProvenanceMeasurement {
	t.Helper()
	got, err := MeasureProvenance(context.Background(), sbomRepo(t, files))
	if err != nil {
		t.Fatalf("MeasureProvenance: %v", err)
	}
	return got
}

// Positive: this repository's release generates provenance with this tool and signs it with
// cosign attest-blob in the job that ran the build, which is Level 2 and no more; the workflow
// says so itself (#330).
func TestMeasureProvenanceReadsTheEngineReleaseAsLevel2(t *testing.T) {
	got, err := MeasureProvenance(context.Background(), engineRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != SLSABuildL2 || got.LevelWorkflow != "release-binaries.yml" || got.CosignWorkflow != "release-binaries.yml" {
		t.Fatalf("MeasureProvenance(engine) = %+v; want Level 2 and cosign, both from release-binaries.yml", got)
	}
}

// Positive and negative: each step shape measures the level the SLSA v1.0 Build track and
// GitHub's attestation documentation give it.
func TestMeasureProvenanceLevels(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		level int
	}{
		"direct attest-build-provenance is Level 2": {
			map[string]string{releaseWorkflow: sbomJob("      - uses: actions/attest-build-provenance@v4\n        with:\n          subject-path: dist/*\n")}, SLSABuildL2},
		"actions/attest in its default provenance mode is Level 2": {
			map[string]string{releaseWorkflow: sbomJob("      - uses: Actions/Attest@v4\n        with:\n          subject-path: dist/*\n")}, SLSABuildL2},
		"actions/attest with an SLSA predicate type is Level 2": {
			map[string]string{releaseWorkflow: sbomJob("      - uses: actions/attest@v4\n        with:\n          predicate-type: https://slsa.dev/provenance/v1\n          predicate-path: p.json\n")}, SLSABuildL2},
		"actions/attest of an SBOM is no provenance": {
			map[string]string{releaseWorkflow: sbomJob("      - uses: actions/attest@v4\n        with:\n          sbom-path: sbom.json\n")}, 0},
		"actions/attest of a custom predicate is no provenance": {
			map[string]string{releaseWorkflow: sbomJob("      - uses: actions/attest@v4\n        with:\n          predicate-type: https://example.com/test/v1\n          predicate: '{}'\n")}, 0},
		"SLSA generator called by tag is Level 3": {
			map[string]string{releaseWorkflow: reusableJob("slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0")}, SLSABuildL3},
		"local reusable workflow running actions/attest is Level 3": {
			map[string]string{
				releaseWorkflow:               reusableJob("./.github/workflows/build.yml"),
				".github/workflows/build.yml": calledWorkflow("      - run: make dist\n      - uses: actions/attest-build-provenance@v4\n"),
			}, SLSABuildL3},
		"local reusable workflow signing with cosign stays Level 2": {
			map[string]string{
				releaseWorkflow:               reusableJob("./.github/workflows/build.yml"),
				".github/workflows/build.yml": calledWorkflow("      - run: cosign attest-blob --yes --type slsaprovenance1 --predicate p.json dist/a.tgz\n"),
			}, SLSABuildL2},
		"this tool's provenance signed by cosign is Level 2": {
			map[string]string{releaseWorkflow: sbomJob("      - run: |\n          go run ./cmd/standardsctl provenance \\\n            -checksums dist/checksums.txt \\\n            -out dist/p.json\n      - run: cosign attest-blob --yes --statement dist/p.json\n")}, SLSABuildL2},
		"this tool's provenance redirected and signed is Level 2": {
			map[string]string{releaseWorkflow: sbomJob("      - run: |\n          praetorctl provenance -checksums c.txt > p.json\n          cosign attest-blob --yes --statement=p.json\n")}, SLSABuildL2},
		"this tool's provenance unsigned is Level 1": {
			map[string]string{releaseWorkflow: sbomJob("      - run: ./bin/praetorctl provenance -checksums c.txt -out p.json\n")}, SLSABuildL1},
		"a cosign attestation of another file leaves the provenance unsigned": {
			map[string]string{releaseWorkflow: sbomJob("      - run: praetorctl provenance -out p.json\n      - run: cosign attest-blob --yes --statement other.json\n")}, SLSABuildL1},
		"a signature made before the provenance is written does not sign it": {
			map[string]string{releaseWorkflow: sbomJob("      - run: cosign attest-blob --yes --statement p.json\n      - run: praetorctl provenance -out p.json\n")}, SLSABuildL1},
		"a type flag on the next command is not the attestation's": {
			map[string]string{releaseWorkflow: sbomJob("      - run: |\n          cosign attest-blob --yes --predicate sbom.json\n          echo --type slsaprovenance1\n")}, 0},
		"an attestation step named only in a comment does not run": {
			map[string]string{releaseWorkflow: sbomJob("      - run: echo ok # cosign attest --type slsaprovenance1\n")}, 0},
		"a reusable workflow nobody calls is not measured on its own": {
			map[string]string{".github/workflows/build.yml": calledWorkflow("      - uses: actions/attest-build-provenance@v4\n")}, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, tc.files)
			if got.Level != tc.level {
				t.Fatalf("Level = %d (%+v); want %d", got.Level, got, tc.level)
			}
			if tc.level > 0 && got.LevelWorkflow != "release.yml" {
				t.Fatalf("LevelWorkflow = %q; want release.yml", got.LevelWorkflow)
			}
		})
	}
}

// attestStep is GitHub's attestation action over the release archives.
const attestStep = "      - uses: actions/attest-build-provenance@v4\n        with:\n          subject-path: dist/*.tar.gz\n"

// buildsInTheCallerRelease builds and uploads the archives in its own job, then has the
// reusable workflow attest.yml attest them.
const buildsInTheCallerRelease = "on:\n  push:\n    tags: ['v*']\njobs:\n" +
	"  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make dist\n" +
	"      - uses: actions/upload-artifact@v7\n        with:\n          name: dist\n          path: dist\n" +
	"  attest:\n    needs: [build]\n    uses: ./.github/workflows/attest.yml\n"

// A reusable workflow of this repository is Level 3 only when the job that attests also built
// what it attests (GitHub: "The reusable workflow you use to build your software must also
// generate artifact attestations"). Positive: a build step before the attestation, and a
// download after it, are Level 3. Negative: a reusable workflow that attests what the caller
// built and uploaded, or that builds nothing, or builds only after attesting, is Level 2 and is
// named with the reason (review of #330).
func TestMeasureProvenanceCreditsLevel3OnlyWhenTheReusableWorkflowBuilds(t *testing.T) {
	download := "      - uses: actions/download-artifact@v8\n        with:\n          name: dist\n"
	cases := map[string]struct {
		caller, steps string
		level         int
		reason        string
	}{
		"go build, then the attestation":                                 {"", "      - run: GOOS=linux go build -o dist/app ./cmd/app\n" + attestStep, SLSABuildL3, ""},
		"GoReleaser action release, then the attestation":                {"", goreleaserStep + attestStep, SLSABuildL3, ""},
		"an image build, then actions/attest":                            {"", "      - uses: docker/build-push-action@v7\n      - uses: actions/attest@v4\n        with:\n          subject-name: ghcr.io/acme/app\n          subject-digest: sha256:abc\n", SLSABuildL3, ""},
		"a download after the attestation does not undo it":              {"", "      - run: make dist\n" + attestStep + download, SLSABuildL3, ""},
		"the caller builds, the reusable workflow downloads and attests": {buildsInTheCallerRelease, download + attestStep, SLSABuildL2, importedAttestation},
		"a download before the build still taints the attestation":       {"", download + "      - run: make dist\n" + attestStep, SLSABuildL2, importedAttestation},
		"gh run download before the attestation":                         {"", "      - run: |\n          make dist\n          gh run download \"$RUN_ID\" -n dist\n" + attestStep, SLSABuildL2, importedAttestation},
		"no build step at all":                                           {"", "      - uses: actions/checkout@v7\n" + attestStep, SLSABuildL2, unbuiltAttestation},
		"the build after the attestation":                                {"", attestStep + "      - run: make dist\n", SLSABuildL2, unbuiltAttestation},
		"GoReleaser check is no build":                                   {"", "      - uses: goreleaser/goreleaser-action@v7\n        with:\n          args: check\n" + attestStep, SLSABuildL2, unbuiltAttestation},
		"a build named only in a comment":                                {"", "      - run: echo ok # go build ./...\n" + attestStep, SLSABuildL2, unbuiltAttestation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, map[string]string{
				releaseWorkflow:                cmp.Or(tc.caller, reusableJob("./.github/workflows/attest.yml")),
				".github/workflows/attest.yml": calledWorkflow(tc.steps),
			})
			uncredited := strings.Join(got.Uncredited, "\n")
			if got.Level != tc.level || got.LevelWorkflow != "release.yml" {
				t.Fatalf("MeasureProvenance = %+v; want Level %d from release.yml", got, tc.level)
			}
			if tc.reason == "" && uncredited != "" {
				t.Fatalf("Uncredited = %q; want none at Level 3", uncredited)
			}
			want := "release.yml: ./.github/workflows/attest.yml: its attestation is Level 2, because " + tc.reason
			if tc.reason != "" && !strings.Contains(uncredited, want) {
				t.Fatalf("Uncredited = %q; want it to contain %q", uncredited, want)
			}
		})
	}
}

// Negative: a reusable workflow that cannot be read, or the SLSA generator called by anything
// but a vX.Y.Z tag, earns no level and is named with the reason.
func TestMeasureProvenanceNamesUncreditedCalls(t *testing.T) {
	cases := map[string]struct {
		files  map[string]string
		reason string
	}{
		"another repository's reusable workflow": {
			map[string]string{releaseWorkflow: reusableJob("acme/shared/.github/workflows/build.yml@v1")},
			"release.yml: acme/shared/.github/workflows/build.yml@v1: a reusable workflow in another repository is not read"},
		"the SLSA generator called by digest": {
			map[string]string{releaseWorkflow: reusableJob("slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@0123456789abcdef0123456789abcdef01234567")},
			"verifies only when it is called by a vX.Y.Z tag"},
		"a generator-repository workflow that builds nothing": {
			map[string]string{releaseWorkflow: reusableJob("slsa-framework/slsa-github-generator/.github/workflows/pre-submit.lint.yml@v2.1.0")},
			"is not read"},
		"a reusable workflow called from a reusable workflow": {
			map[string]string{
				releaseWorkflow:               reusableJob("./.github/workflows/build.yml"),
				".github/workflows/build.yml": "on: workflow_call\njobs:\n  inner:\n    uses: ./.github/workflows/sign.yml\n",
				".github/workflows/sign.yml":  calledWorkflow("      - uses: actions/attest-build-provenance@v4\n"),
			},
			"./.github/workflows/sign.yml: a reusable workflow called from a reusable workflow is not followed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, tc.files)
			if got.Level != 0 || !strings.Contains(strings.Join(got.Uncredited, "\n"), tc.reason) {
				t.Fatalf("MeasureProvenance = %+v; want Level 0 and an uncredited call containing %q", got, tc.reason)
			}
		})
	}
}

// Positive and negative: a cosign signing step is any cosign sign, sign-blob, attest or
// attest-blob, or a GoReleaser release whose signing blocks run cosign over some artifacts.
// Installing cosign, verifying with it, or skipping GoReleaser's signing signs nothing.
func TestMeasureProvenanceFindsCosignSigning(t *testing.T) {
	release := "      - uses: goreleaser/goreleaser-action@v7\n        with:\n          args: release --clean\n"
	cases := map[string]struct {
		files  map[string]string
		signed bool
	}{
		"cosign sign-blob":                        {map[string]string{releaseWorkflow: sbomJob("      - run: /usr/local/bin/cosign sign-blob --yes --bundle a.sigstore.json a.tgz\n")}, true},
		"cosign sign of an image":                 {map[string]string{releaseWorkflow: sbomJob("      - run: cosign sign --yes ghcr.io/acme/app@sha256:abc\n")}, true},
		"goreleaser signs with cosign":            {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "signs:\n  - cmd: cosign\n    artifacts: checksum\n"}, true},
		"goreleaser binary_signs with cosign":     {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "binary_signs:\n  - cmd: cosign\n"}, true},
		"goreleaser docker_signs default":         {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "docker_signs:\n  - ids: [app]\n"}, true},
		"installing cosign only":                  {map[string]string{releaseWorkflow: sbomJob("      - uses: sigstore/cosign-installer@v4.1.2\n")}, false},
		"verifying only":                          {map[string]string{releaseWorkflow: sbomJob("      - run: cosign verify-blob --bundle a.sigstore.json a.tgz\n")}, false},
		"goreleaser signs default artifacts none": {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "signs:\n  - cmd: cosign\n"}, false},
		"goreleaser signs with gpg":               {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "signs:\n  - artifacts: all\n"}, false},
		"goreleaser docker_signs off":             {map[string]string{releaseWorkflow: sbomJob(release), ".goreleaser.yaml": "docker_signs:\n  - artifacts: none\n"}, false},
		"goreleaser release skipping sign":        {map[string]string{releaseWorkflow: sbomJob("      - run: goreleaser release --clean --skip=sbom,sign\n"), ".goreleaser.yaml": "signs:\n  - cmd: cosign\n    artifacts: checksum\n"}, false},
		"goreleaser check only":                   {map[string]string{releaseWorkflow: sbomJob("      - run: goreleaser check\n"), ".goreleaser.yaml": "signs:\n  - cmd: cosign\n    artifacts: checksum\n"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, tc.files)
			if (got.CosignWorkflow == "release.yml") != tc.signed || (!tc.signed && got.CosignWorkflow != "") {
				t.Fatalf("CosignWorkflow = %q; want signed=%t", got.CosignWorkflow, tc.signed)
			}
		})
	}
}

// Boundary: a repository without workflows measures Level 0 without error; an empty, jobless or
// malformed workflow, or a call to a reusable workflow the repository does not hold, fails
// closed rather than measuring what it cannot read.
func TestMeasureProvenanceBoundaries(t *testing.T) {
	got, err := MeasureProvenance(context.Background(), sbomRepo(t, map[string]string{"README.md": "x\n"}))
	if err != nil || got.Level != 0 || got.LevelWorkflow != "" || got.CosignWorkflow != "" {
		t.Fatalf("no workflows: %+v, %v; want Level 0 and no error", got, err)
	}
	failures := map[string]struct {
		files map[string]string
		want  string
	}{
		"empty workflow":     {map[string]string{releaseWorkflow: ""}, "release.yml declares no jobs"},
		"comment-only file":  {map[string]string{releaseWorkflow: "# release\n"}, "release.yml declares no jobs"},
		"malformed workflow": {map[string]string{releaseWorkflow: "jobs: [unclosed\n"}, "workflow release.yml"},
		"scalar document":    {map[string]string{releaseWorkflow: "release\n"}, "workflow release.yml"},
		"missing reusable workflow": {map[string]string{releaseWorkflow: reusableJob("./.github/workflows/build.yml")},
			"calls ./.github/workflows/build.yml, which is no reusable workflow"},
		"unreadable goreleaser config": {map[string]string{
			releaseWorkflow:    sbomJob("      - run: goreleaser release\n"),
			".goreleaser.yaml": "signs: {unclosed\n",
		}, "parse .goreleaser.yaml"},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			_, err := MeasureProvenance(context.Background(), sbomRepo(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MeasureProvenance error = %v; want one containing %q", err, tc.want)
			}
		})
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := MeasureProvenance(nil, engineRoot); err == nil {
		t.Fatal("MeasureProvenance(nil context) succeeded; want an error")
	}
}

// Each GoReleaser command of a run: script is read on its own (review of #330). Positive: a
// release after a check in the same script signs with cosign and generates the SBOM its
// configuration declares. Negative: goreleaser check followed by another command that names
// release is no release, so the configuration's cosign signing and SBOM do not count. Boundary: a
// release an echo prints runs nothing.
func TestGoreleaserCommandsAreReadOneAtATime(t *testing.T) {
	config := "signs:\n  - cmd: cosign\n    artifacts: checksum\nsboms:\n  - artifacts: archive\n"
	cases := map[string]struct {
		run      string
		releases bool
	}{
		"check, then a release":             {"      - run: |\n          goreleaser check\n          ./bin/goreleaser release --clean\n", true},
		"check, then gh release create":     {"      - run: |\n          goreleaser check\n          gh release create \"$TAG\" dist/*\n", false},
		"check && a command naming release": {"      - run: goreleaser check && gh release create v1\n", false},
		"check; a command naming release":   {"      - run: goreleaser check; gh release create v1\n", false},
		"a find -exec escape ends nothing":  {"      - run: find dist -name '*.tgz' -exec ls {} \\; ; goreleaser release --clean\n", true},
		"a release an echo prints":          {"      - run: echo goreleaser release --clean\n", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{releaseWorkflow: sbomJob(tc.run), ".goreleaser.yaml": config}
			if got := measure(t, files); (got.CosignWorkflow != "") != tc.releases {
				t.Fatalf("CosignWorkflow = %q; want a release that signs: %t", got.CosignWorkflow, tc.releases)
			}
			sbom, err := SBOMWorkflow(context.Background(), sbomRepo(t, files))
			if err != nil || (sbom != "") != tc.releases {
				t.Fatalf("SBOMWorkflow = %q, %v; want a release that generates an SBOM: %t", sbom, err, tc.releases)
			}
		})
	}
}

// make is the build of a reusable workflow only when it runs a target (review of #330).
// Positive: a target, after options with values, a job count and a variable assignment.
// Negative: a bare make, one that prints its version, one in dry-run mode. Boundary: options
// and assignments alone name no target.
func TestMeasureProvenanceNeedsAMakeTarget(t *testing.T) {
	cases := map[string]struct {
		run   string
		level int
	}{
		"a target":                            {"make dist", SLSABuildL3},
		"options, a count, then a target":     {"make -C build -j 4 VERSION=1.2.3 dist", SLSABuildL3},
		"a bare make":                         {"make", SLSABuildL2},
		"make printing its version":           {"make --version", SLSABuildL2},
		"make in dry-run mode":                {"make -n dist", SLSABuildL2},
		"options and assignments, no target":  {"make -C build -j 4 VERSION=1.2.3", SLSABuildL2},
		"a target an echo prints is no build": {"echo make dist", SLSABuildL2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, map[string]string{
				releaseWorkflow:                reusableJob("./.github/workflows/attest.yml"),
				".github/workflows/attest.yml": calledWorkflow("      - run: " + tc.run + "\n" + attestStep),
			})
			if got.Level != tc.level {
				t.Fatalf("MeasureProvenance = %+v; want Level %d", got, tc.level)
			}
		})
	}
}

// A cache restore of the files a reusable workflow attests brings in what an earlier run built,
// like a download (review of #330). Positive: a cache outside the workspace or of another
// directory leaves the attestation Level 3. Negative: a cache of the attested directory, or of a
// file below the attested pattern, makes it Level 2 and names the reason. Boundary: an
// attestation by digest, or of a workspace-wide pattern, cannot be told apart from the cache, so
// any restore makes it Level 2.
func TestMeasureProvenanceCountsACacheRestoreOfTheAttestedFiles(t *testing.T) {
	cache := func(action, path string) string {
		return "      - uses: " + action + "\n        with:\n          key: k\n          path: " + path + "\n"
	}
	build := "      - run: make dist\n"
	attestDigest := "      - uses: actions/attest@v4\n        with:\n          subject-name: ghcr.io/acme/app\n          subject-digest: sha256:abc\n"
	attestAll := "      - uses: actions/attest-build-provenance@v4\n        with:\n          subject-path: '*.tar.gz'\n"
	cases := map[string]struct {
		steps string
		level int
	}{
		"a module cache outside the workspace":      {cache("actions/cache@v5", "~/go/pkg/mod") + build + attestStep, SLSABuildL3},
		"a cache of another directory":              {cache("actions/cache/restore@v5", "node_modules") + build + attestStep, SLSABuildL3},
		"a cache of the attested directory":         {cache("actions/cache/restore@v5", "dist") + build + attestStep, SLSABuildL2},
		"a cache of a file below the pattern":       {cache("actions/cache@v5", "./dist/app.tar.gz") + build + attestStep, SLSABuildL2},
		"an attestation by digest after a cache":    {cache("actions/cache@v5", "node_modules") + build + attestDigest, SLSABuildL2},
		"a workspace-wide pattern after a cache":    {cache("actions/cache@v5", "vendor") + build + attestAll, SLSABuildL2},
		"a cache restore after the attestation":     {build + attestStep + cache("actions/cache/restore@v5", "dist"), SLSABuildL3},
		"a negated cache path excludes the subject": {cache("actions/cache@v5", "'!dist'") + build + attestStep, SLSABuildL3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, map[string]string{
				releaseWorkflow:                reusableJob("./.github/workflows/attest.yml"),
				".github/workflows/attest.yml": calledWorkflow(tc.steps),
			})
			if got.Level != tc.level {
				t.Fatalf("MeasureProvenance = %+v; want Level %d", got, tc.level)
			}
			if tc.level == SLSABuildL2 && !strings.Contains(strings.Join(got.Uncredited, "\n"), importedAttestation) {
				t.Fatalf("Uncredited = %q; want the import named", got.Uncredited)
			}
		})
	}
}

// What never runs signs and attests nothing (review of #330). Negative: an attestation in a step
// or job whose if: is the literal false, and cosign named only in an echo or printf, count for
// nothing. Positive: a condition that may hold is read as running, and a command after an echo
// in the same script runs.
func TestMeasureProvenanceSkipsWhatNeverRuns(t *testing.T) {
	attest := "uses: actions/attest-build-provenance@v4\n"
	cases := map[string]struct {
		workflow string
		level    int
		signed   bool
	}{
		"a step with if: false":       {sbomJob("      - if: false\n        " + attest), 0, false},
		"a job with if: ${{ false }}": {"on: push\njobs:\n  release:\n    if: ${{ false }}\n    runs-on: ubuntu-latest\n    steps:\n      - " + attest, 0, false},
		"a condition that may hold":   {sbomJob("      - if: startsWith(github.ref, 'refs/tags/')\n        " + attest), SLSABuildL2, false},
		"cosign an echo prints":       {sbomJob("      - run: echo cosign sign is todo\n"), 0, false},
		"cosign a printf prints":      {sbomJob("      - run: printf 'cosign sign-blob a.tgz'\n"), 0, false},
		"cosign after an echo":        {sbomJob("      - run: echo signing && cosign sign-blob --yes a.tgz\n"), 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := measure(t, map[string]string{releaseWorkflow: tc.workflow})
			if got.Level != tc.level || (got.CosignWorkflow != "") != tc.signed {
				t.Fatalf("MeasureProvenance = %+v; want Level %d and signed=%t", got, tc.level, tc.signed)
			}
		})
	}
}
