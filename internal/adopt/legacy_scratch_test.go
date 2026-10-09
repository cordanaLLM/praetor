package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

// legacyProbe is a path inside the legacy scratch root; a directory-only rule matches it
// whether or not the root exists.
const legacyProbe = state.LegacyWorkingDirName + "/evidence/private.md"

// legacyIgnored asks Git, with the global configuration isolated, whether the repository's
// own rules ignore legacyProbe.
func legacyIgnored(t *testing.T, root string) bool {
	t.Helper()
	ignored, err := util.GitIgnoredPaths(t.Context(), root, []string{legacyProbe}, true)
	if err != nil {
		t.Fatal(err)
	}
	return slices.Contains(ignored, legacyProbe)
}

// reconcileIgnore runs the one managed-block writer adoption and EnsurePrivateIgnore share.
func reconcileIgnore(t *testing.T, root string) string {
	t.Helper()
	if _, _, err := writeManagedGitIgnore(t.Context(), root, adoptGitIgnoreSeed, false, false); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, filepath.Join(root, gitIgnoreFile))
}

// Positive (#641): a repository that retired .workingdir2 keeps it visible to Git. It deletes
// the rule from the managed block once; with no such root on disk, adoption leaves it out and
// audit's block and probes agree. With git-ignore declined, operator rules without it pass.
func TestLegacyScratch_Positive_RetiredRootStaysVisible(t *testing.T) {
	requireGit(t)
	root := newTestRepo(t, "legacy-retired")
	adopted := reconcileIgnore(t, root)
	retired := strings.Replace(adopted, legacyScratchIgnore+"\n", "", 1)
	if retired == adopted || !strings.HasSuffix(retired, RetiredLegacyScratchBlock()) {
		t.Fatalf("adoption wrote no %s rule to retire:\n%s", legacyScratchIgnore, adopted)
	}
	mustWrite(t, filepath.Join(root, gitIgnoreFile), retired)
	if again := reconcileIgnore(t, root); again != retired || legacyIgnored(t, root) {
		t.Fatalf("adoption restored the retired rule:\n%s", again)
	}
	if !HasManagedGitIgnoreTail(root, retired) {
		t.Fatal("audit still demands the retired rule in the managed block")
	}
	if roots := PrivateScratchRoots(root, retired, false); !slices.Equal(roots, []string{state.WorkingDirName}) {
		t.Fatalf("audit probes %v after retirement, want the working directory alone", roots)
	}
	if roots := PrivateScratchRoots(root, "/.workingdir/\n", true); !slices.Equal(roots, []string{state.WorkingDirName}) {
		t.Fatalf("declined audit probes %v, want the working directory alone", roots)
	}
}

// Negative: the rule is the default. A fresh adoption ignores .workingdir2 before any such root
// exists, so one created after adoption never reaches the index unnoticed. While the root is on
// disk, adoption restores a deleted rule and audit demands it, with or without the managed block.
func TestLegacyScratch_Negative_DefaultAndPresentRootAreIgnored(t *testing.T) {
	requireGit(t)
	root := newTestRepo(t, "legacy-present")
	got := reconcileIgnore(t, root)
	if got != adoptGitIgnoreSeed+"\n"+ManagedGitIgnoreBlock() || !legacyIgnored(t, root) {
		t.Fatalf("a fresh adoption left %s visible to Git:\n%s", state.LegacyWorkingDirName, got)
	}
	if roots := PrivateScratchRoots(root, got, false); !slices.Equal(roots, EveryPrivateScratchRoot()) {
		t.Fatalf("audit probes %v after a fresh adoption, want both roots", roots)
	}
	mustWrite(t, filepath.Join(root, state.LegacyWorkingDirName, "OPEN.md"), "private\n")
	withoutRule := strings.Replace(got, legacyScratchIgnore+"\n", "", 1)
	if HasManagedGitIgnoreTail(root, withoutRule) {
		t.Fatal("audit accepts a block without the rule while the root is present")
	}
	for _, declined := range []bool{false, true} {
		if roots := PrivateScratchRoots(root, withoutRule, declined); !slices.Equal(roots, EveryPrivateScratchRoot()) {
			t.Fatalf("declined=%t: audit probes %v while the root is present, want both roots", declined, roots)
		}
	}
	mustWrite(t, filepath.Join(root, gitIgnoreFile), withoutRule)
	if restored := reconcileIgnore(t, root); restored != got {
		t.Fatalf("adoption did not restore the rule for a present root:\n%s", restored)
	}
}

// legacyRuleCase is the expected keepsLegacyScratch answer for one .gitignore text, with the
// managed block in use and with git-ignore declined.
type legacyRuleCase struct{ managed, declined bool }

// legacyRuleCases returns the rule spellings the boundary test checks: the exact rule keeps it,
// in either line-ending style and at either end of the file, with or without the managed block.
// Once a readable block lacks it, other spellings are operator patterns and keep nothing. A file
// without a readable block gets the default, unless git-ignore is declined.
func legacyRuleCases() map[string]legacyRuleCase {
	retired := RetiredLegacyScratchBlock()
	cases := map[string]legacyRuleCase{
		"":                                     {true, false},
		"/.workingdir2/":                       {true, true},
		"bin/\r\n/.workingdir2/\r\n":           {true, true},
		"bin/\n/.workingdir2/":                 {true, true},
		retired:                                {false, false},
		util.RestoreLineEndings(retired, true): {false, false},
		retired + "/.workingdir2/\n":           {true, true},
		"a/\n" + gitIgnoreManagedBegin + "\nb/\n":              {true, false},
		util.RestoreLineEndings(ManagedGitIgnoreBlock(), true): {true, true},
	}
	for _, spelling := range []string{" /.workingdir2/", ".workingdir2/", "/.workingdir2", "!/.workingdir2/", "# /.workingdir2/", "/.workingdir2/evidence/", "/.workingdir22/"} {
		cases[spelling+"\n"] = legacyRuleCase{true, false}
		cases[spelling+"\n\n"+retired] = legacyRuleCase{false, false}
	}
	return cases
}

// Boundary: keepsLegacyScratch answers each spelling as legacyRuleCases states, and a present
// root keeps the rule whatever the text says.
func TestLegacyScratch_Boundary_ExactRuleAndBlock(t *testing.T) {
	for text, want := range legacyRuleCases() {
		if got := keepsLegacyScratch(text, false, false); got != want.managed {
			t.Errorf("keepsLegacyScratch(%q, managed) = %t, want %t", text, got, want.managed)
		}
		if got := keepsLegacyScratch(text, false, true); got != want.declined {
			t.Errorf("keepsLegacyScratch(%q, declined) = %t, want %t", text, got, want.declined)
		}
	}
	for _, declined := range []bool{false, true} {
		if !keepsLegacyScratch(RetiredLegacyScratchBlock(), true, declined) {
			t.Errorf("declined=%t: a present root without a rule was not kept", declined)
		}
	}
}

// Boundary: a retired block stays retired through a merge and still carries the Kconfig
// .config negation once written; audit accepts it in either form while the root is absent and
// refuses both once it is present, and a present root brings the rule back beside the negation.
func TestLegacyScratch_Boundary_RetiredBlockKeepsConfigNegation(t *testing.T) {
	root := t.TempDir()
	retired, negated := RetiredLegacyScratchBlock(), renderManagedGitIgnore(false, true)
	for _, block := range []string{retired, negated} {
		if merged, err := mergeGitIgnoreRules(block, false, false); err != nil || merged != block {
			t.Fatalf("a merge changed the retired block:\n%s\n%v", merged, err)
		}
		if !HasManagedGitIgnoreTail(root, block) {
			t.Fatalf("audit refused the retired block:\n%s", block)
		}
	}
	if merged, err := mergeGitIgnoreRules(negated, true, false); err != nil || merged != renderManagedGitIgnore(true, true) {
		t.Fatalf("a present root did not restore the rule beside the negation:\n%s\n%v", merged, err)
	}
	mustWrite(t, filepath.Join(root, state.LegacyWorkingDirName), "")
	for _, block := range []string{retired, negated} {
		if HasManagedGitIgnoreTail(root, block) {
			t.Fatalf("audit accepted a retired block while the root is present:\n%s", block)
		}
	}
}

// Boundary: any entry named .workingdir2 counts as present, a dangling symbolic link included,
// and so does one that cannot be inspected; a longer name does not. Dropping the rule never
// changes the shared rule list.
func TestLegacyScratch_Boundary_EntryNameAndRuleList(t *testing.T) {
	assertLegacyScratchPresentBoundary(t)
	assertPrivateIgnoreRulesBoundary(t)
}

func assertLegacyScratchPresentBoundary(t *testing.T) {
	t.Helper()
	for name, want := range map[string]bool{"file": true, "dir": true, "longer": false, "absent": false} {
		root := t.TempDir()
		switch name {
		case "file":
			mustWrite(t, filepath.Join(root, state.LegacyWorkingDirName), "")
		case "dir":
			mustWrite(t, filepath.Join(root, state.LegacyWorkingDirName, "OPEN.md"), "")
		case "longer":
			mustWrite(t, filepath.Join(root, state.LegacyWorkingDirName+"x", "OPEN.md"), "")
		}
		if got := legacyScratchPresent(root); got != want {
			t.Errorf("%s: legacyScratchPresent = %t, want %t", name, got, want)
		}
	}
	if !legacyScratchPresent("uninspectable\x00root") {
		t.Error("an entry that cannot be inspected counted as absent")
	}
}

func assertPrivateIgnoreRulesBoundary(t *testing.T) {
	t.Helper()
	before := slices.Clone(managedIgnoreRules)
	if rules := privateIgnoreRules(managedIgnoreRules, false); slices.Contains(rules, legacyScratchIgnore) || len(rules) != len(before)-1 {
		t.Fatalf("privateIgnoreRules(false) = %v", rules)
	}
	if !slices.Equal(managedIgnoreRules, before) || !slices.Equal(privateIgnoreRules(managedIgnoreRules, true), before) {
		t.Fatalf("privateIgnoreRules changed the shared rule list: %v", managedIgnoreRules)
	}
}

// Boundary: a dangling symbolic link named .workingdir2 is an entry on disk, so the rule stays.
func TestLegacyScratch_Boundary_DanglingLinkIsPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, state.LegacyWorkingDirName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !legacyScratchPresent(root) || !keepsLegacyScratch(RetiredLegacyScratchBlock(), legacyScratchPresent(root), false) {
		t.Fatal("a dangling link named .workingdir2 dropped the rule")
	}
}
