package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseFlowOrder is the order the release markers must appear in: the GoReleaser release
// into a draft, the provenance attestation, its verification, then publication.
var releaseFlowOrder = [...]string{"release", "attest", "verify", "publish"}

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
	ids := make([]string, 0, len(spec.Jobs))
	for id := range spec.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		violations = append(violations, releaseJobViolations(ids[i], spec.Jobs[ids[i]].Steps)...)
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

// releaseJobViolations checks the step order of one release job.
func releaseJobViolations(id string, steps []workflowStep) []string {
	position := map[string]int{"release": -1, "attest": -1, "verify": -1, "publish": -1}
	var violations []string
	for i := 0; i < len(steps) && i < maxStepsPerJob; i++ {
		text := releaseStepText(steps[i])
		markers := map[string]bool{
			"release": strings.Contains(text, "goreleaser release"),
			"attest":  strings.Contains(text, "cosign attest-blob"),
			"verify":  strings.Contains(text, "cosign verify-blob-attestation"),
			"publish": strings.Contains(text, "--draft=false"),
		}
		if strings.Contains(text, "gh release upload") && position["publish"] >= 0 {
			violations = append(violations, fmt.Sprintf("job %s step %d uploads after the release was published", id, i+1))
		}
		for marker, present := range markers {
			if present {
				position[marker] = i
			}
		}
		if strings.Contains(text, "syft dir:") {
			violations = append(violations, fmt.Sprintf("job %s step %d catalogues the source tree", id, i+1))
		}
	}
	if position["release"] < 0 {
		return violations
	}
	for i := 1; i < len(releaseFlowOrder); i++ {
		if position[releaseFlowOrder[i]] <= position[releaseFlowOrder[i-1]] {
			violations = append(violations, fmt.Sprintf("job %s: %s does not follow %s", id, releaseFlowOrder[i], releaseFlowOrder[i-1]))
		}
	}
	return violations
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
