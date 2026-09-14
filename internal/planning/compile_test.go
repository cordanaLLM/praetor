package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/valid-draft.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixtureDraft(t *testing.T) Draft {
	t.Helper()
	var draft Draft
	if err := json.Unmarshal(fixtureBytes(t), &draft); err != nil {
		t.Fatal(err)
	}
	return draft
}

func compileDraft(t *testing.T, draft Draft) (*Result, error) {
	t.Helper()
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	return Compile(t.Context(), raw)
}

func TestCompileDeterministicRoundTrip(t *testing.T) {
	first, err := Compile(t.Context(), fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(t.Context(), first.Files["plan.json"])
	if err != nil {
		t.Fatalf("canonical plan.json did not compile: %v", err)
	}
	if first.Draft.ID != "plan-example-workshop" || first.Status != StatusStructurallyValid ||
		!first.ReviewRequired || first.ProvenanceStatus != ProvenanceUnverified || len(first.Digest) != 64 {
		t.Fatalf("truthful result metadata missing: %+v", first)
	}
	if !reflect.DeepEqual(first.MilestoneOrder, []string{"milestone-contract", "milestone-projection"}) ||
		!reflect.DeepEqual(first.StepOrder, []string{"step-research", "step-validate", "step-render"}) {
		t.Fatalf("unexpected deterministic order: milestones=%v steps=%v", first.MilestoneOrder, first.StepOrder)
	}
	if first.Digest != second.Digest || !reflect.DeepEqual(first.Files, second.Files) {
		t.Fatal("canonical plan round trip changed digest or artifacts")
	}
	assertArtifacts(t, first)
}

func TestCompileNormalizesSetAndGraphOrder(t *testing.T) {
	draft := fixtureDraft(t)
	reverseSources(draft.Sources)
	reverseRequirements(draft.Requirements)
	reverseMilestones(draft.Milestones)
	reverseSteps(draft.Steps)
	reordered, err := compileDraft(t, draft)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := Compile(t.Context(), fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Digest != canonical.Digest || !reflect.DeepEqual(reordered.Files, canonical.Files) {
		t.Fatal("equivalent graph order changed canonical output")
	}
}

func TestStepOrderIncludesMilestoneDependencies(t *testing.T) {
	draft := fixtureDraft(t)
	// The projection step has no explicit step edge. Its milestone dependency
	// still places every contract step before it.
	draft.Steps[0].DependsOn = []string{}
	result, err := compileDraft(t, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.StepOrder, []string{"step-research", "step-validate", "step-render"}) {
		t.Fatalf("milestone dependency missing from step order: %v", result.StepOrder)
	}
}

func TestCompileCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(ctx, fixtureBytes(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled compile returned %v", err)
	}
	var nilContext context.Context
	if _, err := Compile(nilContext, fixtureBytes(t)); err == nil {
		t.Fatal("nil context accepted")
	}
}

func assertArtifacts(t *testing.T, result *Result) {
	t.Helper()
	wantNames := []string{"MILESTONES.md", "ROADMAP.md", "TODO.md", "plan.json"}
	for _, name := range wantNames {
		data, exists := result.Files[name]
		if !exists || len(data) == 0 || len(data) > MaxArtifactBytes {
			t.Fatalf("missing or invalid artifact %s", name)
		}
	}
	if len(result.Files) != len(wantNames) {
		t.Fatalf("unexpected artifact set: %v", result.Files)
	}
	for _, name := range wantNames[:3] {
		data := result.Files[name]
		if !bytes.Contains(data, []byte("draft proposal; structurally valid; review required")) ||
			!bytes.Contains(data, []byte("source-adapter byte verification")) || !bytes.Contains(data, []byte(result.Digest)) {
			t.Fatalf("artifact %s overstates status or lacks cross-file identity", name)
		}
	}
	if !bytes.Contains(result.Files["TODO.md"], []byte("ROADMAP.md#"+anchorID("roadmap", "roadmap-render"))) ||
		!bytes.Contains(result.Files["ROADMAP.md"], []byte("Caller-asserted unverified source")) ||
		!bytes.Contains(result.Files["MILESTONES.md"], []byte("TODO [`todo-render`]")) {
		t.Fatal("derived proposal cross-links are incomplete")
	}
}

func TestReportUsesOneDeterministicAdapterContract(t *testing.T) {
	result, err := Compile(t.Context(), fixtureBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	report := result.Report(true)
	if report.SchemaVersion != SchemaVersion || report.DraftID != result.Draft.ID || report.Digest != result.Digest ||
		!report.ReviewRequired || !report.ArtifactsWritten || report.ProvenanceStatus != ProvenanceUnverified {
		t.Fatalf("incomplete shared report: %+v", report)
	}
	want := []string{"MILESTONES.md", "ROADMAP.md", "TODO.md", "plan.json"}
	if !reflect.DeepEqual(report.Artifacts, want) {
		t.Fatalf("artifact names are not stable: %v", report.Artifacts)
	}
	result.Files["AAA.md"] = []byte("fixture")
	if got := result.Report(false).Artifacts; !reflect.DeepEqual(got, []string{"AAA.md", "MILESTONES.md", "ROADMAP.md", "TODO.md", "plan.json"}) {
		t.Fatalf("artifact names are not sorted: %v", got)
	}
}

func TestRenderedTextAndAnchorsAreInert(t *testing.T) {
	draft := fixtureDraft(t)
	draft.Project.Title = `<script>alert(1)</script> [fake](https://invalid) # heading`
	oldMilestoneID := draft.Milestones[0].ID
	draft.Milestones[0].ID = "Milestone:A/B.C"
	draft.Milestones[1].DependsOn[0] = draft.Milestones[0].ID
	for index := range draft.Steps {
		if draft.Steps[index].MilestoneID == oldMilestoneID {
			draft.Steps[index].MilestoneID = draft.Milestones[0].ID
			draft.Steps[index].Link.MilestoneID = draft.Milestones[0].ID
		}
	}
	draft.Steps[0].Link.TODOID = "TODO:A/B.C"
	draft.Steps[0].Link.RoadmapID = "Roadmap:A/B.C"
	draft.Steps[1].Link.RoadmapID = "Roadmap:A:B.C"
	draft.Steps[0].Detail = `<script>alert(2)</script> [detail](https://invalid)`
	result, err := compileDraft(t, draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TODO.md", "ROADMAP.md", "MILESTONES.md"} {
		data := string(result.Files[name])
		if strings.Contains(data, "<script>") || strings.Contains(data, "[fake](") || strings.Contains(data, "[detail](") {
			t.Fatalf("artifact %s rendered active untrusted Markdown or HTML: %s", name, data)
		}
	}
	roadmapAnchor := anchorID("roadmap", draft.Steps[0].Link.RoadmapID)
	collidingRoadmapAnchor := anchorID("roadmap", draft.Steps[1].Link.RoadmapID)
	milestoneAnchor := anchorID("milestone", draft.Milestones[0].ID)
	todoAnchor := anchorID("todo", draft.Steps[0].Link.TODOID)
	if roadmapAnchor == collidingRoadmapAnchor || roadmapAnchor == milestoneAnchor ||
		!bytes.Contains(result.Files["ROADMAP.md"], []byte(`id="`+roadmapAnchor+`"`)) ||
		!bytes.Contains(result.Files["ROADMAP.md"], []byte(`id="`+collidingRoadmapAnchor+`"`)) ||
		!bytes.Contains(result.Files["MILESTONES.md"], []byte(`id="`+milestoneAnchor+`"`)) ||
		!bytes.Contains(result.Files["TODO.md"], []byte(`id="`+todoAnchor+`"`)) {
		t.Fatal("mixed-case punctuation IDs did not produce distinct deterministic anchors")
	}
}

func reverseSources(values []Source) { reflect.Swapper(values)(0, len(values)-1) }

func reverseRequirements(values []Requirement) { reflect.Swapper(values)(0, len(values)-1) }

func reverseMilestones(values []Milestone) { reflect.Swapper(values)(0, len(values)-1) }

func reverseSteps(values []Step) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func TestCompileStrictJSON(t *testing.T) {
	raw := fixtureBytes(t)
	cases := map[string][]byte{
		"unknown":   bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"unknown": true, "schema_version": 1`), 1),
		"duplicate": bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1),
		"trailing":  append(append([]byte(nil), raw...), []byte(` {}`)...),
		"invalid":   {0xff},
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile(t.Context(), candidate); err == nil {
				t.Fatal("ambiguous JSON accepted")
			}
		})
	}
	exact := append(append([]byte(nil), raw...), bytes.Repeat([]byte(" "), MaxJSONBytes-len(raw))...)
	if _, err := Compile(t.Context(), exact); err != nil {
		t.Fatalf("exact JSON byte boundary rejected: %v", err)
	}
	if _, err := Compile(t.Context(), append(exact, ' ')); err == nil {
		t.Fatal("over JSON byte boundary accepted")
	}
}

func TestInstructionsRemainInertData(t *testing.T) {
	draft := fixtureDraft(t)
	draft.Steps[0].Actions[0] = "Record the literal text $(external-command) without interpreting it."
	result, err := compileDraft(t, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Files["ROADMAP.md"]), markdownText("$(external-command)")) {
		t.Fatal("inert instruction text was not preserved as data")
	}
}
