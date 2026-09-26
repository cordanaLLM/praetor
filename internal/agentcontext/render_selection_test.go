package agentcontext

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: a selection emits exactly the named clients' projections, in registry order, and
// names the rest as not applicable. Another vendor's section still stays out of a selected
// projection, because ownership is decided by the whole registry, not by the selection.
func TestCompileContent_Positive_ClientSelectionLimitsProjections(t *testing.T) {
	tr := NewTranspiler()
	tr.Clients = []string{" Codex", "claude"}
	result, err := tr.CompileContent(vendorFixture)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, file := range result.Files {
		got = append(got, file.RelativePath)
	}
	if want := []string{"CLAUDE.md", ".codex/rules.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("projections = %v, want %v", got, want)
	}
	wantExcluded := []string{".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md", ".windsurfrules", ".gemini/GEMINI.md"}
	if !reflect.DeepEqual(result.NotApplicable, wantExcluded) {
		t.Fatalf("not applicable = %v, want %v", result.NotApplicable, wantExcluded)
	}
	if strings.Contains(result.Files[0].Content, "Use the rules pane.") {
		t.Fatal("the unselected Cursor section leaked into CLAUDE.md")
	}
}

// Negative: an id that names no projection fails the whole compilation and names both the
// typo and the supported ids, instead of quietly emitting the known subset.
func TestCompileContent_Negative_UnknownClientRejected(t *testing.T) {
	tr := NewTranspiler()
	tr.Clients = []string{"claude", "vim"}
	result, err := tr.CompileContent(vendorFixture)
	if err == nil || result != nil {
		t.Fatalf("unknown client accepted: result=%v err=%v", result, err)
	}
	for _, want := range []string{"vim", strings.Join(Clients(), ", ")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// Boundary: nil keeps every projection (the behaviour before selection existed), an empty
// list emits none, and a list longer than the bound is rejected before it is scanned.
func TestCompileContent_Boundary_ClientSelectionAbsentEmptyOversized(t *testing.T) {
	tr := NewTranspiler()
	all, err := tr.CompileContent(vendorFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Files) != len(vendorTargets) || all.NotApplicable != nil {
		t.Fatalf("nil selection: %d files, not applicable %v", len(all.Files), all.NotApplicable)
	}

	tr.Clients = []string{}
	none, err := tr.CompileContent(vendorFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Files) != 0 || len(none.NotApplicable) != len(vendorTargets) {
		t.Fatalf("empty selection: %d files, %d not applicable", len(none.Files), len(none.NotApplicable))
	}

	tr.Clients = make([]string, maxSelectedClients+1)
	for i := range tr.Clients {
		tr.Clients[i] = "claude"
	}
	if _, err := tr.CompileContent(vendorFixture); err == nil {
		t.Fatal("oversized selection accepted")
	}
	tr.Clients = tr.Clients[:maxSelectedClients]
	if got, err := tr.CompileContent(vendorFixture); err != nil || len(got.Files) != 1 {
		t.Fatalf("selection at the bound: err=%v", err)
	}
}

// Positive: PersonaDirs keeps the selected clients' persona directories in registry order and
// names the other clients' directories as left out.
func TestPersonaDirs_Positive_SelectionSplitsDirectories(t *testing.T) {
	selected, excluded, err := PersonaDirs([]string{"Codex ", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".claude/agents", ".codex/agents"}; !reflect.DeepEqual(selected, want) {
		t.Fatalf("selected = %v, want %v", selected, want)
	}
	if want := []string{".github/agents", ".gemini/agents"}; !reflect.DeepEqual(excluded, want) {
		t.Fatalf("excluded = %v, want %v", excluded, want)
	}
}

// Negative: an unknown id fails the persona selection the same way it fails the context files.
func TestPersonaDirs_Negative_UnknownClientRejected(t *testing.T) {
	selected, excluded, err := PersonaDirs([]string{"gemini", "vim"})
	if err == nil || selected != nil || excluded != nil || !strings.Contains(err.Error(), "vim") {
		t.Fatalf("PersonaDirs = %v, %v, %v; want an error naming vim", selected, excluded, err)
	}
}

// Boundary: nil keeps all four directories, an empty list keeps none, and clients that read no
// personas (Cursor, Windsurf) add nothing to either list.
func TestPersonaDirs_Boundary_AbsentEmptyAndPersonalessClients(t *testing.T) {
	all, none, err := PersonaDirs(nil)
	if err != nil || len(all) != 4 || none != nil {
		t.Fatalf("nil selection = %v, %v, %v", all, none, err)
	}
	kept, left, err := PersonaDirs([]string{})
	if err != nil || kept != nil || !reflect.DeepEqual(left, all) {
		t.Fatalf("empty selection = %v, %v, %v", kept, left, err)
	}
	kept, left, err = PersonaDirs([]string{"cursor", "windsurf"})
	if err != nil || kept != nil || !reflect.DeepEqual(left, all) {
		t.Fatalf("personaless selection = %v, %v, %v", kept, left, err)
	}
}
