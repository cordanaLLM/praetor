package editor

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: a declared list, aliases included, resolves to canonical ids and names every
// other supported editor as not applicable, in the reader-facing order.
func TestSelectEditors_Positive_DeclaredListNarrowsTheSet(t *testing.T) {
	got, err := SelectEditors([]string{"code", "nvim", "vscode"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{EditorNeovim, EditorVSCode}; !reflect.DeepEqual(got.Editors, want) {
		t.Fatalf("editors = %v, want %v", got.Editors, want)
	}
	if len(got.NotApplicable) != len(supportedEditorIDs)-2 || got.NotApplicable[0] != EditorUniversal {
		t.Fatalf("not applicable = %v", got.NotApplicable)
	}
	for _, id := range got.NotApplicable {
		if id == EditorVSCode || id == EditorNeovim {
			t.Fatalf("selected editor %s reported not applicable", id)
		}
	}
}

// Negative: an unknown id fails the whole selection with the same message --editors gives.
func TestSelectEditors_Negative_UnknownIDRejected(t *testing.T) {
	_, err := SelectEditors([]string{"vscode", "notepad"})
	if err == nil {
		t.Fatal("unknown editor accepted")
	}
	if want := unknownEditorsError([]string{"notepad"}).Error(); err.Error() != want {
		t.Fatalf("error %q, want %q", err, want)
	}
}

// Boundary: nil keeps every supported editor; an empty list selects none and names all of them;
// the full list leaves nothing not applicable; a list above the loop bound is refused.
func TestSelectEditors_Boundary_AbsentEmptyFullOversized(t *testing.T) {
	all, err := SelectEditors(nil)
	if err != nil || !reflect.DeepEqual(all.Editors, DefaultOptions().Editors) || all.NotApplicable != nil {
		t.Fatalf("nil selection = %+v, %v", all, err)
	}
	none, err := SelectEditors([]string{})
	if err != nil || none.Editors == nil || len(none.Editors) != 0 || !reflect.DeepEqual(none.NotApplicable, supportedEditorIDs) {
		t.Fatalf("empty selection = %+v, %v", none, err)
	}
	full, err := SelectEditors(supportedEditorIDs)
	if err != nil || len(full.Editors) != len(supportedEditorIDs) || len(full.NotApplicable) != 0 {
		t.Fatalf("full selection = %+v, %v", full, err)
	}
	oversized := strings.Split(strings.Repeat("vscode,", maxLoopBound+1), ",")[:maxLoopBound+1]
	if _, err := SelectEditors(oversized); err == nil {
		t.Fatal("oversized selection accepted")
	}
	if got, err := SelectEditors(oversized[:maxLoopBound]); err != nil || len(got.Editors) != 1 {
		t.Fatalf("selection at the bound = %+v, %v", got, err)
	}
}
