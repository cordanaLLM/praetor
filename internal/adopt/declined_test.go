package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var knownArtifacts = []string{"manifest", "lockfile", "baseline", "readme", "editors", "dev-container"}

// Positive: the artefact named in #145 can be declined, which is the whole point.
func TestDeclinedArtifacts_Positive_AcceptsADeclaredArtefact(t *testing.T) {
	declined, err := declinedArtifacts([]string{"readme"}, knownArtifacts)
	if err != nil {
		t.Fatalf("declining readme: %v", err)
	}
	if !declined["readme"] {
		t.Error("readme was declared but is not declined")
	}
	if declined["editors"] {
		t.Error("an undeclared artefact is declined")
	}
}

func TestArtifactDeclined_3D_UsesCanonicalAdoptionPolicy(t *testing.T) {
	declined, err := ArtifactDeclined([]string{"  README  "}, "readme")
	if err != nil || !declined {
		t.Fatalf("canonical README decline: declined=%v err=%v", declined, err)
	}
	if _, err := ArtifactDeclined([]string{"read-me"}, "readme"); err == nil || !strings.Contains(err.Error(), "unknown artefact") {
		t.Fatalf("unknown decline must not be silently ignored: %v", err)
	}
	if declined, err := ArtifactDeclined(nil, "readme"); err != nil || declined {
		t.Fatalf("empty boundary: declined=%v err=%v", declined, err)
	}
	oversized := make([]string, maxDeclinedArtifacts+1)
	if _, err := ArtifactDeclined(oversized, "readme"); err == nil {
		t.Fatal("oversized decline boundary was accepted")
	}
}

func TestDeclinedArtifacts_Positive_IsCaseAndSpaceInsensitive(t *testing.T) {
	declined, err := declinedArtifacts([]string{"  README  ", "Dev-Container"}, knownArtifacts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !declined["readme"] || !declined["dev-container"] {
		t.Errorf("declared artefacts not normalised: %v", declined)
	}
}

// Negative: a name matching no artefact is an error, not a no-op. A silently ignored typo
// reads exactly like a working declaration until the artefact it was meant to suppress
// reappears, which is the failure mode this whole check exists to avoid.
func TestDeclinedArtifacts_Negative_RejectsAnUnknownName(t *testing.T) {
	_, err := declinedArtifacts([]string{"read-me"}, knownArtifacts)
	if err == nil {
		t.Fatal("an unknown artefact name was accepted silently")
	}
	if !strings.Contains(err.Error(), "read-me") || !strings.Contains(err.Error(), "readme") {
		t.Errorf("error names neither the typo nor the alternatives: %v", err)
	}
}

// Negative: declining the manifest would leave a repository claiming adoption while
// carrying nothing that records what it adopted.
func TestDeclinedArtifacts_Negative_RejectsMandatoryArtefacts(t *testing.T) {
	for name := range mandatoryArtifacts {
		if _, err := declinedArtifacts([]string{name}, knownArtifacts); err == nil {
			t.Errorf("%q was declinable but must not be", name)
		}
	}
}

// Boundary: nothing declared, empty entries, and the upper bound.
func TestDeclinedArtifacts_Boundary_EmptyAndOversized(t *testing.T) {
	declined, err := declinedArtifacts(nil, knownArtifacts)
	if err != nil || len(declined) != 0 {
		t.Errorf("no declaration should decline nothing: %v %v", declined, err)
	}
	if declined, err = declinedArtifacts([]string{"", "   "}, knownArtifacts); err != nil || len(declined) != 0 {
		t.Errorf("blank entries should decline nothing: %v %v", declined, err)
	}
	oversized := make([]string, maxDeclinedArtifacts+1)
	for i := range oversized {
		oversized[i] = "readme"
	}
	if _, err := declinedArtifacts(oversized, knownArtifacts); err == nil {
		t.Error("an oversized decline list was accepted")
	}
}

// Every step name the chain declares must be declinable or explicitly mandatory; a name in
// neither set is one a repository can never refuse and never discover.
func TestAdoptStepNames_Boundary_AreAllAddressable(t *testing.T) {
	names := adoptStepNames()
	if len(names) < 15 {
		t.Fatalf("expected the full adoption chain, got %d steps", len(names))
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate step name %q: two artefacts would share one declaration", name)
		}
		seen[name] = true
		if name != strings.ToLower(name) || strings.TrimSpace(name) != name {
			t.Errorf("step name %q is not in the form a manifest would carry", name)
		}
	}
	for name := range mandatoryArtifacts {
		if !seen[name] {
			t.Errorf("mandatory artefact %q names no step in the chain", name)
		}
	}
}

func TestLoadDeclaredManifest_Negative_UnknownKey(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, ".standards.yaml")
	if err := os.WriteFile(path, []byte("unknown_key: true\nadoption:\n  decline: [readme]"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	_, err := loadDeclaredManifest(context.Background(), tempDir)
	if err == nil {
		t.Fatal("expected error on unknown key, got nil")
	}
}

func TestLoadDeclaredManifest_Negative_SecondDocument(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, ".standards.yaml")
	if err := os.WriteFile(path, []byte("adoption:\n  decline: [readme]\n---\nadoption:\n  decline: []"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	_, err := loadDeclaredManifest(context.Background(), tempDir)
	if err == nil {
		t.Fatal("expected error on second document, got nil")
	}
}

// TestLoadDeclaredManifest_Boundary_MissingAndValid: no manifest is no decision, and a valid
// manifest yields its declines; only an unreadable one is an error.
func TestLoadDeclaredManifest_Boundary_MissingAndValid(t *testing.T) {
	tempDir := t.TempDir()
	manifest, err := loadDeclaredManifest(context.Background(), tempDir)
	if err != nil || manifest != nil {
		t.Fatalf("missing manifest = (%v, %v), want (nil, nil)", manifest, err)
	}
	path := filepath.Join(tempDir, ".standards.yaml")
	if err := os.WriteFile(path, []byte("adoption:\n  decline: [git-ignore]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err = loadDeclaredManifest(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	if got := manifestDeclines(manifest); len(got) != 1 || got[0] != "git-ignore" {
		t.Fatalf("declines = %v, want [git-ignore]", got)
	}
}

// RepositoryArtifactDeclined reads adoption.decline from the manifest at the repository root:
// the step it names is declined, and another step is not.
func TestRepositoryArtifactDeclined_Positive_ReadsTheManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, manifestFile), "version: 1\nadoption:\n  decline: [branch-ruleset]\n")
	if declined, err := RepositoryArtifactDeclined(t.Context(), root, "branch-ruleset"); err != nil || !declined {
		t.Fatalf("branch-ruleset: declined=%v err=%v, want declined", declined, err)
	}
	if declined, err := RepositoryArtifactDeclined(t.Context(), root, "labels"); err != nil || declined {
		t.Fatalf("labels: declined=%v err=%v, want not declined", declined, err)
	}
}

// A manifest that does not decode, and a decline list naming no adoption step, are errors, so a
// writer outside adoption fails closed instead of writing what the repository may have declined.
func TestRepositoryArtifactDeclined_Negative_FailsClosed(t *testing.T) {
	for name, manifest := range map[string]string{
		"undecodable manifest": "version: [\n",
		"unknown step":         "version: 1\nadoption:\n  decline: [no-such-step]\n",
	} {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, manifestFile), manifest)
		if declined, err := RepositoryArtifactDeclined(t.Context(), root, "branch-ruleset"); err == nil || declined {
			t.Errorf("%s: declined=%v err=%v, want an error", name, declined, err)
		}
	}
}

// A repository without a manifest declines nothing, and a cancelled context reads nothing.
func TestRepositoryArtifactDeclined_Boundary_NoManifestAndCancelledContext(t *testing.T) {
	root := t.TempDir()
	if declined, err := RepositoryArtifactDeclined(t.Context(), root, "branch-ruleset"); err != nil || declined {
		t.Fatalf("no manifest: declined=%v err=%v, want nothing declined", declined, err)
	}
	mustWrite(t, filepath.Join(root, manifestFile), "version: 1\nadoption:\n  decline: [branch-ruleset]\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if declined, err := RepositoryArtifactDeclined(ctx, root, "branch-ruleset"); err == nil || declined {
		t.Fatalf("cancelled context: declined=%v err=%v, want an error", declined, err)
	}
}

// runOrSkipStep runs a step the manifest does not decline and records it completed.
func TestRunOrSkipStep_Positive_RunsAndRecordsCompleted(t *testing.T) {
	s := &adoptSession{repoPath: t.TempDir(), report: &AdoptReport{}}
	ran := false
	step := namedStep{name: "readme", run: func(context.Context, *adoptSession) error { ran = true; return nil }}
	if err := runOrSkipStep(t.Context(), s, step, false); err != nil {
		t.Fatalf("runOrSkipStep: %v", err)
	}
	if !ran || len(s.report.Steps) != 1 || s.report.Steps[0].Status != StepCompleted {
		t.Fatalf("ran=%v steps=%+v, want one completed readme step", ran, s.report.Steps)
	}
}

// A step that fails returns its error and is recorded failed, not completed.
func TestRunOrSkipStep_Negative_FailedStepIsRecordedFailed(t *testing.T) {
	s := &adoptSession{repoPath: t.TempDir(), report: &AdoptReport{}}
	boom := errors.New("boom")
	step := namedStep{name: "readme", run: func(context.Context, *adoptSession) error { return boom }}
	if err := runOrSkipStep(t.Context(), s, step, false); !errors.Is(err, boom) {
		t.Fatalf("runOrSkipStep error = %v, want %v", err, boom)
	}
	if len(s.report.Steps) != 1 || s.report.Steps[0].Status != StepFailed {
		t.Fatalf("steps=%+v, want one failed readme step", s.report.Steps)
	}
}

// A declined step never runs, even one that would fail, and is recorded declined with the
// decline named in the report.
func TestRunOrSkipStep_Boundary_DeclinedStepDoesNotRun(t *testing.T) {
	s := &adoptSession{repoPath: t.TempDir(), report: &AdoptReport{}}
	step := namedStep{name: "readme", run: func(context.Context, *adoptSession) error {
		t.Fatal("a declined step ran")
		return nil
	}}
	if err := runOrSkipStep(t.Context(), s, step, true); err != nil {
		t.Fatalf("runOrSkipStep: %v", err)
	}
	if len(s.report.Steps) != 1 || s.report.Steps[0].Status != StepDeclined {
		t.Fatalf("steps=%+v, want one declined readme step", s.report.Steps)
	}
	if len(s.report.ActionDetails) != 1 || !strings.Contains(s.report.ActionDetails[0].Details, "adoption.decline") {
		t.Fatalf("action details=%+v, want the decline named", s.report.ActionDetails)
	}
}
