package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// flowMarker names one step a release job must contain and how its command text shows it.
type flowMarker struct {
	name  string
	match func(text string) bool
}

// containsAll returns a matcher for step text that carries every one of parts.
func containsAll(parts ...string) func(string) bool {
	return func(text string) bool {
		for i := 0; i < len(parts); i++ {
			if !strings.Contains(text, parts[i]) {
				return false
			}
		}
		return true
	}
}

var (
	releaseMarker = flowMarker{"release", containsAll("goreleaser release")}
	publishMarker = flowMarker{"publish", containsAll("--draft=false")}
)

// releaseFlowOrder is the order the release markers must appear in: the GoReleaser release
// into a draft, the provenance attestation, its verification, then publication.
var releaseFlowOrder = [...]flowMarker{
	releaseMarker,
	{"attest", containsAll("cosign attest-blob")},
	{"verify", containsAll("cosign verify-blob-attestation")},
	publishMarker,
}

// containerFlowOrder is the order the image and chart markers must appear in, between the
// GoReleaser release that pushes and signs the image and publication (ADR-0013): the image
// signature verified on its pushed digest, the SBOM and provenance attestations of that
// digest checked, the chart pushed and signed on the digest helm push reports, in one step
// so nothing else is signed, and that signature verified. A failure in any of them must
// leave the release a draft.
var containerFlowOrder = [...]flowMarker{
	releaseMarker,
	{"image-verify", containsAll("cosign verify ", "\"$IMAGE@")},
	{"image-attestations", containsAll("imagetools inspect \"$IMAGE@", "json .SBOM", "json .Provenance")},
	{"chart-sign", containsAll("helm push", "cosign sign ", "$CHART_REPOSITORY")},
	{"chart-verify", containsAll("cosign verify ", "CHART_REPOSITORY#oci://}/praetor@")},
	publishMarker,
}

// markerPositions records the last step each marker matches, -1 for a marker no step
// matches.
func markerPositions(steps []workflowStep, order []flowMarker) map[string]int {
	position := make(map[string]int, len(order))
	for i := 0; i < len(order); i++ {
		position[order[i].name] = -1
	}
	for i := 0; i < len(steps) && i < maxStepsPerJob; i++ {
		text := releaseStepText(steps[i])
		for j := 0; j < len(order); j++ {
			if order[j].match(text) {
				position[order[j].name] = i
			}
		}
	}
	return position
}

// orderViolations reports every marker of order that does not follow the one before it in
// a job that runs a GoReleaser release; a job without one has no order to keep.
func orderViolations(id string, steps []workflowStep, order []flowMarker) []string {
	position := markerPositions(steps, order)
	if position[releaseMarker.name] < 0 {
		return nil
	}
	var violations []string
	for i := 1; i < len(order); i++ {
		if position[order[i].name] <= position[order[i-1].name] {
			violations = append(violations, fmt.Sprintf("job %s: %s does not follow %s", id, order[i].name, order[i-1].name))
		}
	}
	return violations
}

// releaseFlowViolations names every way one release workflow could publish before its
// assets are complete or ship an SBOM of the source tree. The publish step must come after
// the GoReleaser release, the provenance signing and its verification; the GoReleaser
// configuration must leave the release a draft; no step may upload after publication; and no
// step may catalogue the checkout with syft dir:. (#43, #315).
func releaseFlowViolations(workflow, goreleaser []byte) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(workflow, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	var config struct {
		Release struct {
			Draft bool `yaml:"draft"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(goreleaser, &config); err != nil {
		return nil, fmt.Errorf("parse goreleaser: %w", err)
	}
	var violations []string
	if !config.Release.Draft {
		violations = append(violations, "goreleaser publishes the release itself; release.draft must be true")
	}
	ids := sortedJobIDs(spec.Jobs)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		violations = append(violations, releaseJobViolations(ids[i], spec.Jobs[ids[i]].Steps)...)
	}
	return violations, nil
}

// containerFlowViolations names every release job that could publish before the image and
// chart it ships are pushed, signed and verified on their digests.
func containerFlowViolations(workflow []byte) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(workflow, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	ids := sortedJobIDs(spec.Jobs)
	var violations []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		violations = append(violations, orderViolations(ids[i], spec.Jobs[ids[i]].Steps, containerFlowOrder[:])...)
	}
	return violations, nil
}

// releaseStepText is the command text of one step, with a GoReleaser action's args spelled
// as the command line they run.
func releaseStepText(step workflowStep) string {
	text := step.Run
	if args, ok := step.With["args"].(string); ok && strings.HasPrefix(step.Uses, goreleaserActionPath+"@") {
		text += "\ngoreleaser " + args
	}
	return text
}

// releaseJobViolations checks the step order of one release job, and that no step uploads
// after publication or catalogues the source tree.
func releaseJobViolations(id string, steps []workflowStep) []string {
	var violations []string
	published := false
	for i := 0; i < len(steps) && i < maxStepsPerJob; i++ {
		text := releaseStepText(steps[i])
		if strings.Contains(text, "gh release upload") && published {
			violations = append(violations, fmt.Sprintf("job %s step %d uploads after the release was published", id, i+1))
		}
		published = published || publishMarker.match(text)
		if strings.Contains(text, "syft dir:") {
			violations = append(violations, fmt.Sprintf("job %s step %d catalogues the source tree", id, i+1))
		}
	}
	return append(violations, orderViolations(id, steps, releaseFlowOrder[:])...)
}

// Positive: the shipped release flow drafts, attests, verifies and only then publishes, and
// no second tag workflow races it.
func TestEngineReleaseFlowPublishesLast(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	goreleaser, err := os.ReadFile(filepath.Join(engineRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	violations, err := releaseFlowViolations(workflows["release-binaries.yml"], goreleaser)
	if err != nil || len(violations) != 0 {
		t.Fatalf("release flow: violations %v, err %v", violations, err)
	}
	if _, present := workflows["sbom.yml"]; present {
		t.Fatal("sbom.yml is back: a second tag workflow races the release and uploads after publication")
	}
}

// Negative and boundary: an auto-published release, an upload after publication, a
// source-tree SBOM and a publish before verification each fail on their own; a workflow
// without a release step has no order to check.
func TestReleaseFlowViolationsSyntheticShapes(t *testing.T) {
	const draft = "release:\n  draft: true\n"
	step := func(run string) string { return "      - run: " + run + "\n" }
	job := func(steps ...string) []byte {
		return []byte("on: [push]\njobs:\n  release:\n    steps:\n" + strings.Join(steps, ""))
	}
	good := []string{
		step("goreleaser release --clean"),
		step("cosign attest-blob --statement p --bundle b"),
		step("cosign verify-blob-attestation --bundle b a"),
		step("gh release edit v1 --draft=false"),
	}
	cases := []struct {
		name       string
		workflow   []byte
		goreleaser string
		want       int
	}{
		{"shipped order", job(good...), draft, 0},
		{"auto-published release", job(good...), "release:\n  draft: false\n", 1},
		{"upload after publication", job(good[0], good[1], good[2], good[3], step("gh release upload v1 late.json")), draft, 1},
		{"source-tree sbom", job(step("syft dir:. -o spdx-json=x"), good[0], good[1], good[2], good[3]), draft, 1},
		{"publish before verification", job(good[0], good[1], good[3], good[2]), draft, 1},
		{"no release step", job(step("echo nothing")), draft, 0},
	}
	for _, tc := range cases {
		violations, err := releaseFlowViolations(tc.workflow, []byte(tc.goreleaser))
		if err != nil || len(violations) != tc.want {
			t.Errorf("%s: violations %v, err %v, want %d", tc.name, violations, err, tc.want)
		}
	}
	if _, err := releaseFlowViolations([]byte("jobs: [\n"), []byte(draft)); err == nil {
		t.Error("malformed workflow accepted")
	}
}

// Positive: the shipped release job verifies the image signature on its pushed digest,
// checks that digest's SBOM and provenance attestations, then pushes, signs and verifies the
// chart, all before it publishes.
func TestEngineReleaseFlowVerifiesImageAndChartBeforePublishing(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	violations, err := containerFlowViolations(workflows["release-binaries.yml"])
	if err != nil || len(violations) != 0 {
		t.Fatalf("container flow: violations %v, err %v", violations, err)
	}
}

// Negative and boundary: an image verified by tag rather than digest, attestations never
// checked, only the SBOM checked, or checked in the signature step itself, a chart signed in
// a step apart from its push and a publish before the chart signature is verified each fail;
// a workflow without a release step has no order to check.
func TestContainerFlowViolationsSyntheticShapes(t *testing.T) {
	step := func(run string) string { return "      - run: '" + run + "'\n" }
	job := func(steps ...string) []byte {
		return []byte("on: [push]\njobs:\n  release:\n    steps:\n" + strings.Join(steps, ""))
	}
	release := step("goreleaser release --clean")
	imageVerify := step(`cosign verify --certificate-identity s "$IMAGE@$digest"`)
	attestations := step(`docker buildx imagetools inspect "$IMAGE@$d" --format "{{ json .SBOM }}" && docker buildx imagetools inspect "$IMAGE@$d" --format "{{ json .Provenance }}"`)
	chartSign := step(`helm push c.tgz "$CHART_REPOSITORY" && cosign sign --yes "${CHART_REPOSITORY#oci://}/praetor@$d"`)
	chartVerify := step(`cosign verify --certificate-identity s "${CHART_REPOSITORY#oci://}/praetor@$CHART_DIGEST"`)
	publish := step("gh release edit v1 --draft=false")
	cases := []struct {
		name     string
		workflow []byte
		want     int
	}{
		{"shipped order", job(release, imageVerify, attestations, chartSign, chartVerify, publish), 0},
		{"image verified by tag", job(release, step(`cosign verify "$IMAGE:$version"`), attestations, chartSign, chartVerify, publish), 1},
		{"attestations never checked", job(release, imageVerify, chartSign, chartVerify, publish), 1},
		{"only the SBOM checked", job(release, imageVerify, step(`docker buildx imagetools inspect "$IMAGE@$d" --format "{{ json .SBOM }}"`),
			chartSign, chartVerify, publish), 1},
		{"attestations checked in the signature step", job(release,
			step(`cosign verify s "$IMAGE@$d" && docker buildx imagetools inspect "$IMAGE@$d" --format "{{ json .SBOM }} {{ json .Provenance }}"`),
			chartSign, chartVerify, publish), 1},
		{"chart signed apart from its push", job(release, imageVerify, attestations, step(`helm push c.tgz "$CHART_REPOSITORY"`),
			step(`cosign sign --yes "${CHART_REPOSITORY#oci://}/praetor@$d"`), chartVerify, publish), 1},
		{"publish before chart verification", job(release, imageVerify, attestations, chartSign, publish, chartVerify), 1},
		{"no release step", job(step("echo nothing")), 0},
	}
	for _, tc := range cases {
		violations, err := containerFlowViolations(tc.workflow)
		if err != nil || len(violations) != tc.want {
			t.Errorf("%s: violations %v, err %v, want %d", tc.name, violations, err, tc.want)
		}
	}
	if _, err := containerFlowViolations([]byte("jobs: [\n")); err == nil {
		t.Error("malformed workflow accepted")
	}
}
