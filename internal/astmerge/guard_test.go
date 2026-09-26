package astmerge

import (
	"fmt"
	"strings"
	"testing"
)

// requireGuardConflict fails unless the guard rejects merged with a conflict on symbol.
func requireGuardConflict(t *testing.T, base, ours, theirs, merged, symbol string) Conflict {
	t.Helper()
	for _, c := range guardConflicts(base, ours, theirs, merged) {
		if c.Symbol == symbol && c.Kind == kindSemantic {
			return c
		}
	}
	t.Fatalf("expected a semantic conflict on %s, got %+v\n%s", symbol, guardConflicts(base, ours, theirs, merged), merged)
	return Conflict{}
}

// requireGuardPass fails when the guard rejects merged.
func requireGuardPass(t *testing.T, base, ours, theirs, merged string) {
	t.Helper()
	if conflicts := guardConflicts(base, ours, theirs, merged); len(conflicts) != 0 {
		t.Fatalf("expected the guard to accept the merge, got %+v\n%s", conflicts, merged)
	}
}

func TestCollectFacts_Positive_ValuesOrderAndErrors(t *testing.T) {
	facts := collectFacts(`package facts

import "time"

type Level int8

const (
	Low Level = iota
	High
	Wait = time.Second
	_    = 3
)

var b = a + 1
var a = seed()
var _ = seed()

func seed() int { return undefinedHelper() }
`)
	want := map[string]string{"Low": "0:facts.Level", "High": "1:facts.Level", "Wait": unknownValue}
	for name, value := range want {
		if facts.consts[name] != value {
			t.Errorf("const %s = %q, want %q", name, facts.consts[name], value)
		}
	}
	if _, blank := facts.consts["_"]; blank || len(facts.consts) != len(want) {
		t.Errorf("expected exactly %v, got %v", want, facts.consts)
	}
	if got := strings.Join(facts.init, ";"); got != "a;b;_ = seed()" {
		t.Errorf("init order = %q, want dependency order a;b then the blank initializer", got)
	}
	if !facts.errs["undefined: undefinedHelper"] || !facts.errs["undefined: time.Second"] {
		t.Errorf("expected the sibling-file and import references as errors, got %v", facts.errs)
	}
}

func TestCollectFacts_Negative_UnparsableSource(t *testing.T) {
	facts := collectFacts("package broken\n\nfunc {")
	if len(facts.errs) != 1 || len(facts.consts) != 0 || facts.init != nil {
		t.Fatalf("expected one parse error and no facts, got %+v", facts)
	}
	for msg := range facts.errs {
		if !strings.HasPrefix(msg, "parse: ") || strings.Contains(msg, guardFile+":") {
			t.Errorf("expected a position-free parse error, got %q", msg)
		}
	}
}

func TestCollectFacts_Boundary_EmptyAndRepeatedInitializers(t *testing.T) {
	if facts := collectFacts("  \n"); len(facts.errs)+len(facts.consts)+len(facts.init) != 0 {
		t.Errorf("an empty source has no facts, got %+v", facts)
	}
	facts := collectFacts("package rep\n\nfunc f() int { return 1 }\n\nvar _ = f()\n\nvar _ = f()\n\nvar x = f()\n")
	if strings.Join(facts.init, ";") != "x" {
		t.Errorf("initializers that recur cannot be matched and must be dropped, got %v", facts.init)
	}
}

// TestNormalizeTypeError_Boundary_ContinuationPositions strips the positions go/types
// writes into a multi-part message, so the same redeclaration compares equal wherever it
// sits in each version.
func TestNormalizeTypeError_Boundary_ContinuationPositions(t *testing.T) {
	first := collectFacts("package dup\n\nconst A = 1\n\nconst A = 2\n")
	second := collectFacts("package dup\n\nfunc F() {}\n\nconst A = 1\n\nfunc G() {}\n\nconst A = 2\n")
	if len(first.errs) == 0 || fmt.Sprint(first.errs) != fmt.Sprint(second.errs) {
		t.Fatalf("expected equal redeclaration errors, got %v and %v", first.errs, second.errs)
	}
}

// TestOfflineImporter_Boundary_PathShapes names every imported package as
// hiss.DefaultImportName derives it from the path, the repository's one path-to-name rule
// (HISS-19): a module major version and a gopkg.in suffix are skipped, and a name no
// identifier can spell, such as go-git's, is kept rather than rewritten into another guess.
func TestOfflineImporter_Boundary_PathShapes(t *testing.T) {
	for path, want := range map[string]string{
		"fmt":                        "fmt",
		"gopkg.in/yaml.v3":           "yaml",
		"github.com/org/module/v2":   "module",
		"github.com/go-git/go-git":   "go-git",
		"github.com/satori/go.uuid":  "go.uuid",
		"v2":                         "v2",
		"github.com/org/v10/subpath": "subpath",
	} {
		pkg, err := offlineImporter{}.Import(path)
		if err != nil || pkg.Path() != path || pkg.Name() != want || !pkg.Complete() {
			t.Errorf("Import(%q) = %v, %v; want a complete package %q named %q", path, pkg, err, path, want)
		}
	}
}

// TestGuard_Positive_UnidentifiableImportNameIsConsistent: an import whose derived name is
// no identifier leaves every version with the same errors, so an unrelated edit on each
// side still merges clean.
func TestGuard_Positive_UnidentifiableImportNameIsConsistent(t *testing.T) {
	base := "package imp\n\nimport \"github.com/go-git/go-git\"\n\nvar repo = git.Open\n"
	ours, theirs := base+"\nfunc Ours() {}\n", base+"\nfunc Theirs() {}\n"
	merged := base + "\nfunc Ours() {}\n\nfunc Theirs() {}\n"
	requireGuardPass(t, base, ours, theirs, merged)
	if facts := collectFacts(merged); len(facts.errs) == 0 {
		t.Fatalf("expected the unresolvable name to report the same errors in every version, got none")
	}
}

// TestGuard_Negative_DroppedImportUnderTheOtherSidesUseFailsClosed: ours removes the
// gopkg.in/yaml.v3 import with its only use while theirs adds another use, so the merged
// file refers to yaml without importing it; the guard reports the new type error.
func TestGuard_Negative_DroppedImportUnderTheOtherSidesUseFailsClosed(t *testing.T) {
	base := "package imp\n\nimport \"gopkg.in/yaml.v3\"\n\nvar enc = yaml.Marshal\n"
	ours := "package imp\n"
	theirs := base + "\nvar dec = yaml.Unmarshal\n"
	requireConflict(t, base, ours, theirs, "typecheck")
}

func TestExpect3_Boundary_AllStateCombinations(t *testing.T) {
	cases := []struct {
		base, ours, theirs, want string
		decided                  bool
	}{
		{"1", "1", "1", "1", true},
		{"1", "2", "2", "2", true},
		{"1", "1", "2", "2", true},
		{"1", "2", "1", "2", true},
		{"1", "2", "3", "", false},
		{absentValue, "1", absentValue, "1", true},
		{"1", absentValue, "1", absentValue, true},
	}
	for _, tc := range cases {
		if got, decided := expect3(tc.base, tc.ours, tc.theirs); got != tc.want || decided != tc.decided {
			t.Errorf("expect3(%s, %s, %s) = %q, %v; want %q, %v", tc.base, tc.ours, tc.theirs, got, decided, tc.want, tc.decided)
		}
	}
}

const guardConstBase = "package g\n\nconst (\n\tA = 1\n\tB = 2\n)\n"

func TestGuard_Negative_ConstValues(t *testing.T) {
	ours := strings.Replace(guardConstBase, "A = 1", "A = 5", 1)
	c := requireGuardConflict(t, guardConstBase, ours, guardConstBase, guardConstBase, "const:A")
	if c.Ours != "5:untyped int" || c.Theirs != "1:untyped int" || c.Base != "1:untyped int" {
		t.Errorf("the conflict must carry every side's value, got %+v", c)
	}
	kept := strings.Replace(guardConstBase, "B = 2", "B = 9", 1)
	requireGuardConflict(t, guardConstBase, guardConstBase+"\nfunc F() {}\n", guardConstBase, kept, "const:B")
	requireGuardConflict(t, guardConstBase, guardConstBase, guardConstBase, guardConstBase+"\nconst C = 3\n", "const:C")
	requireGuardConflict(t, guardConstBase, guardConstBase, "package g\n\nconst A = 1\n", guardConstBase, "const:B")
}

func TestGuard_Positive_ConstValuesFollowTheChangingSide(t *testing.T) {
	ours := strings.Replace(guardConstBase, "A = 1", "A = 5", 1)
	theirs := strings.Replace(guardConstBase, "B = 2", "B = 7", 1)
	merged := strings.Replace(ours, "B = 2", "B = 7", 1)
	requireGuardPass(t, guardConstBase, ours, theirs, merged)
	requireGuardPass(t, "", "package g\n\nconst A = 1\n", "package g\n\nconst B = 2\n", guardConstBase)
}

// TestGuard_Boundary_ConstBothSidesChanged holds a constant both sides changed differently
// to nothing: a derived constant legitimately takes a value neither side had once both of
// its operands change.
func TestGuard_Boundary_ConstBothSidesChanged(t *testing.T) {
	base := "package g\n\nconst (\n\tA = 1\n\tB = 2\n\tSum = A + B\n)\n"
	ours := strings.Replace(base, "A = 1", "A = 10", 1)
	theirs := strings.Replace(base, "B = 2", "B = 20", 1)
	merged := strings.Replace(ours, "B = 2", "B = 20", 1)
	requireGuardPass(t, base, ours, theirs, merged)
}

func TestGuard_Negative_TypeErrorsTheMergeIntroduces(t *testing.T) {
	base := "package g\n\ntype T int\n"
	ours := "package g\n\ntype T int8\n"
	theirs := base + "\nconst X T = 200\n"
	merged := ours + "\nconst X T = 200\n"
	c := requireGuardConflict(t, base, ours, theirs, merged, "typecheck")
	if !strings.Contains(c.Reason, "overflows") {
		t.Errorf("expected the overflow to be named, got %q", c.Reason)
	}
}

// TestGuard_Boundary_ErrorsTheSidesShare accepts a merged file whose only type errors are
// ones a side has too: a reference into a sibling file of the package, or an import.
func TestGuard_Boundary_ErrorsTheSidesShare(t *testing.T) {
	base := "package g\n\nimport \"strings\"\n\nfunc F() string { return strings.ToUpper(sibling) }\n"
	ours := base + "\nfunc G() int { return otherSibling() }\n"
	theirs := base + "\nfunc H() {}\n"
	merged := ours + "\nfunc H() {}\n"
	requireGuardPass(t, base, ours, theirs, merged)
}

const guardVarBase = "package g\n\nfunc rec(s string) int { return len(s) }\n\nvar a = rec(\"a\")\n\nvar b = rec(\"b\")\n"

func TestGuard_Negative_InitOrder(t *testing.T) {
	flipped := "package g\n\nfunc rec(s string) int { return len(s) }\n\nvar b = rec(\"b\")\n\nvar a = rec(\"a\")\n"
	requireGuardConflict(t, guardVarBase, guardVarBase+"\nfunc F() {}\n", guardVarBase, flipped, "init-order")
	requireGuardConflict(t, guardVarBase, flipped, guardVarBase, guardVarBase, "init-order")

	withX := guardVarBase + "\nvar x = rec(\"x\")\n"
	xFirst := strings.Replace(guardVarBase, "var a =", "var x = rec(\"x\")\n\nvar a =", 1)
	requireGuardConflict(t, guardVarBase, withX, guardVarBase, xFirst, "init-order")

	noBase := "package g\n\nfunc rec(s string) int { return len(s) }\n"
	requireGuardConflict(t, noBase, withX, strings.Replace(withX, "var x = rec(\"x\")\n", "", 1)+"\nvar x = rec(\"x\")\n", xFirst, "init-order")
}

func TestGuard_Positive_InitOrderFollowsTheReorderingSide(t *testing.T) {
	flipped := "package g\n\nfunc rec(s string) int { return len(s) }\n\nvar b = rec(\"b\")\n\nvar a = rec(\"a\")\n"
	theirs := guardVarBase + "\nvar c = rec(\"c\")\n"
	requireGuardPass(t, guardVarBase, flipped, theirs, flipped+"\nvar c = rec(\"c\")\n")
}

func TestGuard_Boundary_InitializerBound(t *testing.T) {
	vars := func(n int) string {
		var b strings.Builder
		b.WriteString("package g\n\nfunc f() int { return 1 }\n\nvar (\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "\tv%d = f()\n", i)
		}
		b.WriteString(")\n")
		return b.String()
	}
	atBound := vars(maxGuardInitializers)
	requireGuardPass(t, atBound, atBound, atBound, atBound)
	over := vars(maxGuardInitializers + 1)
	requireGuardConflict(t, over, over, over, over, "init-order")
}

const guardDeclBase = "package g\n\ntype T struct{}\n\nfunc (T) M() {}\n\nvar v = 1\n\nfunc F() {}\n"

// TestGuard_Negative_DeclarationsDroppedOrKept: a declaration one side added must be in the
// merged file and one a side deleted must not; nothing refers to these, so no type error
// would reveal a merge that drops or keeps them.
func TestGuard_Negative_DeclarationsDroppedOrKept(t *testing.T) {
	added := guardDeclBase + "\nvar w = 2\n\nfunc (T) N() {}\n"
	requireGuardConflict(t, guardDeclBase, added, guardDeclBase, guardDeclBase, "decl:w")
	requireGuardConflict(t, guardDeclBase, added, guardDeclBase, guardDeclBase, "decl:T.N")
	withoutF := strings.Replace(guardDeclBase, "\nfunc F() {}\n", "", 1)
	c := requireGuardConflict(t, guardDeclBase, guardDeclBase, withoutF, guardDeclBase, "decl:F")
	if c.Ours != "func" || c.Theirs != absentValue || c.Base != "func" {
		t.Errorf("the conflict must carry every side's state, got %+v", c)
	}
}

// TestGuard_Boundary_DeclarationKindChanges accepts a declaration one side turns into
// another kind, and holds one both sides change differently to nothing.
func TestGuard_Boundary_DeclarationKindChanges(t *testing.T) {
	toFunc := strings.Replace(guardDeclBase, "var v = 1", "func v() {}", 1)
	requireGuardPass(t, guardDeclBase, toFunc, guardDeclBase+"\nfunc G() {}\n", toFunc+"\nfunc G() {}\n")
	toType := strings.Replace(guardDeclBase, "var v = 1", "type v int", 1)
	requireGuardPass(t, guardDeclBase, toFunc, toType, toType)
}

// TestGuardResult_Positive_PassesACleanMergeThrough returns the very result it was given
// when nothing departs, and a conflict without merged code otherwise.
func TestGuardResult_Positive_PassesACleanMergeThrough(t *testing.T) {
	clean := &MergeResult{MergedCode: guardConstBase, Clean: true, ResolvedCount: 3}
	if got := guardResult(clean, guardConstBase, guardConstBase, guardConstBase); got != clean {
		t.Fatalf("expected the clean result unchanged, got %+v", got)
	}
	wrong := &MergeResult{MergedCode: strings.Replace(guardConstBase, "A = 1", "A = 4", 1), Clean: true, ResolvedCount: 3}
	got := guardResult(wrong, guardConstBase, guardConstBase, guardConstBase)
	if got.Clean || got.MergedCode != "" || got.ResolvedCount != 3 || len(got.Conflicts) != 1 {
		t.Fatalf("expected one conflict, no code and the kept resolved count, got %+v", got)
	}
}

// TestMerge_Negative_BothSidesMovingDeclarationsFailClosed: both sides move the var
// declarations, so the merged order starts from base's; the guard rejects the resulting
// initialization order (v1 before v2) that theirs had reversed and ours kept.
func TestMerge_Negative_BothSidesMovingDeclarationsFailClosed(t *testing.T) {
	const header = "package p\n\nvar log []string\n\nfunc rec(s string) int { log = append(log, s); return len(log) }\n"
	base := header + "\nvar (\n\tv1 = rec(\"v1\")\n\tv2 = rec(\"v2\")\n)\n\nfunc F1() {}\n"
	ours := header + "\nfunc F1() {}\n\nvar (\n\tv1 = rec(\"v1\")\n\tv2 = rec(\"v2\")\n)\n"
	theirs := header + "\nvar (\n\tv2 = rec(\"v2\")\n)\n\nvar v1 = rec(\"v1\")\n\nfunc F1() {}\n"
	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if res.Clean || len(res.Conflicts) != 1 || res.Conflicts[0].Symbol != "init-order" {
		t.Fatalf("expected only the init-order conflict, got clean=%v %+v\n%s", res.Clean, res.Conflicts, res.MergedCode)
	}
}

// TestMerge_Negative_CombinationThatOverflowsFailsClosed: each side type-checks on its own,
// but theirs' constant overflows ours' narrower type, so the merged file does not compile
// and the constant has no value where theirs gave it 200.
func TestMerge_Negative_CombinationThatOverflowsFailsClosed(t *testing.T) {
	base := "package g\n\ntype T int\n"
	res, err := Merge(base, "package g\n\ntype T int8\n", base+"\nconst X T = 200\n")
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	var symbols []string
	for _, c := range res.Conflicts {
		symbols = append(symbols, c.Symbol)
	}
	if res.Clean || strings.Join(symbols, ",") != "typecheck,const:X" {
		t.Fatalf("expected the type-check and const:X conflicts, got clean=%v %+v\n%s", res.Clean, res.Conflicts, res.MergedCode)
	}
}
