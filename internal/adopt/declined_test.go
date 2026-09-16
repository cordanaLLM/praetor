package adopt

import (
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
