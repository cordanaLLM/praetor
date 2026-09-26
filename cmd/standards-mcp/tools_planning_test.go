package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/planning"
)

func planningMCPFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "planning", "testdata", "valid-draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPlanningMCPValidatePrepareParityAndReadback(t *testing.T) {
	srv, root := newFixtureServer(t)
	fixture := planningMCPFixture(t)
	writeFixtureFile(t, root, "draft.json", string(fixture))
	var input planning.Draft
	if err := json.Unmarshal(fixture, &input); err != nil {
		t.Fatal(err)
	}
	validated := callTool(t, srv, "standards_planning_validate", map[string]any{"input_path": "draft.json"})
	if validated.IsError {
		t.Fatalf("validation failed: %+v", validated)
	}
	var before planning.Report
	if err := json.Unmarshal([]byte(validated.Content[0].Text), &before); err != nil {
		t.Fatal(err)
	}
	if before.Status != planning.StatusStructurallyValid || !before.ReviewRequired || before.ArtifactsWritten ||
		strings.Contains(validated.Content[0].Text, input.Sources[0].Locator) {
		t.Fatalf("unsafe or incorrect validation metadata: %+v", before)
	}
	if err := os.Mkdir(filepath.Join(root, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	prepared := callTool(t, srv, "standards_planning_prepare", map[string]any{
		"input_path": "draft.json", "output_dir": ".workingdir/planning-draft",
	})
	if prepared.IsError {
		t.Fatalf("prepare failed: %+v", prepared)
	}
	var after planning.Report
	if err := json.Unmarshal([]byte(prepared.Content[0].Text), &after); err != nil {
		t.Fatal(err)
	}
	expected := before
	expected.ArtifactsWritten = true
	if !reflect.DeepEqual(after, expected) {
		t.Fatalf("validate/prepare metadata diverged: before=%+v after=%+v", before, after)
	}
	for _, name := range after.Artifacts {
		data, err := os.ReadFile(filepath.Join(root, ".workingdir/planning-draft", name))
		if err != nil || len(data) == 0 {
			t.Fatalf("artifact %s missing: %v", name, err)
		}
	}
	plan, err := os.ReadFile(filepath.Join(root, ".workingdir/planning-draft/plan.json"))
	var draft planning.Draft
	if err == nil {
		err = json.Unmarshal(plan, &draft)
	}
	if err != nil || draft.ID != input.ID {
		t.Fatalf("canonical plan readback failed: %v", err)
	}
}

func TestPlanningMCPRejectsUnsafeArgumentsPathsAndCollisions(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "draft.json", string(planningMCPFixture(t)))
	if err := os.Mkdir(filepath.Join(root, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFixtureFile(t, outside, "draft.json", string(planningMCPFixture(t)))
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"standards_planning_validate", map[string]any{"input_path": filepath.Join(outside, "draft.json")}},
		{"standards_planning_validate", map[string]any{"input_path": "draft.json", "mode": "apply"}},
		{"standards_planning_prepare", map[string]any{"input_path": "draft.json", "output_dir": "public-plan"}},
		{"standards_planning_prepare", map[string]any{"input_path": "draft.json", "output_dir": filepath.Join(outside, "plan")}},
	} {
		if result := callTool(t, srv, tc.tool, tc.args); !result.IsError {
			t.Fatalf("unsafe call accepted: %s %+v", tc.tool, tc.args)
		}
	}
	permissive, err := NewServerWithOptions(ServerOptions{
		RootDir: root, Version: "v1.0.0-test", AllowOutsideRoot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := callTool(t, permissive, "standards_planning_validate", map[string]any{
		"input_path": filepath.Join(outside, "draft.json"),
	}); !result.IsError {
		t.Fatal("planning tool inherited the server outside-root exemption")
	}
	args := map[string]any{"input_path": "draft.json", "output_dir": ".workingdir/candidate"}
	if result := callTool(t, srv, "standards_planning_prepare", args); result.IsError {
		t.Fatalf("initial prepare failed: %+v", result)
	}
	if result := callTool(t, srv, "standards_planning_prepare", args); !result.IsError {
		t.Fatal("existing artifact directory overwritten")
	}
	if err := os.Symlink(outside, filepath.Join(root, ".workingdir/escape")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	args["output_dir"] = ".workingdir/escape/plan"
	if result := callTool(t, srv, "standards_planning_prepare", args); !result.IsError {
		t.Fatal("escaping output symlink accepted")
	}
	if err := os.Symlink(filepath.Join(outside, "draft.json"), filepath.Join(root, "input-link.json")); err != nil {
		t.Fatal(err)
	}
	if result := callTool(t, srv, "standards_planning_validate", map[string]any{
		"input_path": "input-link.json",
	}); !result.IsError {
		t.Fatal("escaping input symlink accepted")
	}
}

// TestPlanningMCPPrepareBelowSymlinkedPrivateRoot pins the ConfinePath contract that
// privatePlanningOutput depends on: it relates two ConfinePath results (.workingdir and the
// requested output directory), so both must stay lexical. A .workingdir that is a link to
// another directory inside the root is a valid private root, and a new output directory
// below it must be accepted.
func TestPlanningMCPPrepareBelowSymlinkedPrivateRoot(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "draft.json", string(planningMCPFixture(t)))
	if err := os.MkdirAll(filepath.Join(root, ".state", "plan"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".state", filepath.Join(root, ".workingdir")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	result := callTool(t, srv, "standards_planning_prepare", map[string]any{
		"input_path": "draft.json", "output_dir": ".workingdir/plan/candidate",
	})
	if result.IsError {
		t.Fatalf("output below an in-root symlinked .workingdir refused: %+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(root, ".state", "plan", "candidate", "plan.json")); err != nil || len(data) == 0 {
		t.Fatalf("plan.json missing at the link target: %v", err)
	}
}

func TestPlanningMCPInputByteBoundary(t *testing.T) {
	srv, root := newFixtureServer(t)
	fixture := planningMCPFixture(t)
	exact := append(fixture, make([]byte, planning.MaxJSONBytes-len(fixture))...)
	for index := len(fixture); index < len(exact); index++ {
		exact[index] = ' '
	}
	writeFixtureFile(t, root, "draft.json", string(exact))
	args := map[string]any{"input_path": "draft.json"}
	if result := callTool(t, srv, "standards_planning_validate", args); result.IsError {
		t.Fatalf("exact byte boundary rejected: %+v", result)
	}
	writeFixtureFile(t, root, "draft.json", string(append(exact, ' ')))
	if result := callTool(t, srv, "standards_planning_validate", args); !result.IsError {
		t.Fatal("input over byte boundary accepted")
	}
}

func TestPlanningMCPRejectsNullRequiredCollections(t *testing.T) {
	srv, root := newFixtureServer(t)
	fixture := planningMCPFixture(t)
	malformed := strings.Replace(string(fixture), `"depends_on": []`, `"depends_on": null`, 1)
	writeFixtureFile(t, root, "draft.json", malformed)
	result := callTool(t, srv, "standards_planning_validate", map[string]any{"input_path": "draft.json"})
	if !result.IsError {
		t.Fatalf("null required dependency collection accepted: %+v", result)
	}
}
