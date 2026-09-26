package astmerge

import (
	"strings"
	"testing"
)

const importBase = "package imp\n\nimport \"strings\"\n\nfunc Up(s string) string { return strings.ToUpper(s) }\n"

// renamedImport is importBase with the strings import written under name.
func renamedImport(name string) string {
	src := strings.Replace(importBase, "import \"strings\"", "import "+name+" \"strings\"", 1)
	return strings.Replace(src, "strings.ToUpper", name+".ToUpper", 1)
}

// TestMerge_Positive_ImportRenamedOnOneSideMerges: an import only one side renames takes
// that side's name, whichever side it is. Comparing ours with theirs alone reported the
// rename as a conflict with the side that left the import alone.
func TestMerge_Positive_ImportRenamedOnOneSideMerges(t *testing.T) {
	renamed, other := renamedImport("str"), importBase+"\nfunc F() {}\n"
	for name, sides := range map[string][2]string{"ours": {renamed, other}, "theirs": {other, renamed}} {
		t.Run(name, func(t *testing.T) {
			code, _ := mergeClean(t, importBase, sides[0], sides[1])
			if !strings.Contains(code, "str \"strings\"") || !strings.Contains(code, "func F()") {
				t.Errorf("expected the renamed import and the other side's function:\n%s", code)
			}
		})
	}
}

// TestMerge_Negative_ImportRenamedDifferentlyConflicts: both sides renaming one import
// differently conflict, with each side's name and base's for resolution.
func TestMerge_Negative_ImportRenamedDifferentlyConflicts(t *testing.T) {
	res := requireConflict(t, importBase, renamedImport("s1"), renamedImport("s2"), "import:\"strings\"")
	for _, c := range res.Conflicts {
		if c.Symbol == "import:\"strings\"" && (c.Base != "" || c.Ours != "s1" || c.Theirs != "s2") {
			t.Errorf("expected base \"\", ours s1 and theirs s2, got %+v", c)
		}
	}
}

// TestMerge_Negative_ImportRenamedUnderTheOtherSidesUseFailsClosed: theirs adds code that
// uses the import under its old name while ours renames it, so the merged file refers to a
// name it no longer imports; the guard turns that into a conflict.
func TestMerge_Negative_ImportRenamedUnderTheOtherSidesUseFailsClosed(t *testing.T) {
	theirs := importBase + "\nfunc Down(s string) string { return strings.ToLower(s) }\n"
	requireConflict(t, importBase, renamedImport("str"), theirs, "typecheck")
}

// TestMerge_Boundary_ImportBothSidesAdd: an import base lacks and both sides add merges
// clean under one name and conflicts under two, since no base name decides.
func TestMerge_Boundary_ImportBothSidesAdd(t *testing.T) {
	base := "package imp\n"
	withName := func(name string) string {
		return "package imp\n\nimport " + name + " \"strings\"\n\nfunc Up(s string) string { return " + name + ".ToUpper(s) }\n"
	}
	mergeClean(t, base, withName("str"), withName("str"))
	requireConflict(t, base, withName("s1"), withName("s2")+"\nfunc F() {}\n", "import:\"strings\"")
}
