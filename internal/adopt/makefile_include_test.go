package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// mergeDocumentationMakefile is mergeDocumentationMakefileWith where no include is followed; the
// production path always passes the session's expander (documentationMakefile).
func mergeDocumentationMakefile(existing string, force bool) (string, error) {
	return mergeDocumentationMakefileWith(existing, force, noMakefileIncludes)
}

// noMakefileIncludes follows no include: every one stays ambiguous.
func noMakefileIncludes(data string) (string, []string) { return data, nil }

// includeRepo builds a git repository holding files, tracks the names in tracked under the
// hermetic fixture git (timeout-bound), and returns a session over it. The Makefile is only text
// the tests pass to the merge.
func includeRepo(t *testing.T, files map[string]string, tracked ...string) *adoptSession {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
	testsupport.InitGitRepoWithOrigin(t, root, "")
	if len(tracked) > 0 {
		testsupport.RunFixtureGit(t, root, append([]string{"add", "--"}, tracked...))
	}
	return &adoptSession{repoPath: root}
}

func mergeWithIncludes(s *adoptSession, makefile string) (string, error) {
	return mergeDocumentationMakefileWith(makefile, false, s.includeExpander(context.Background()))
}

// TestMergeDocumentationMakefileFollowsTrackedIncludes is the #843 reproduction: tracked fragments
// that define no docs gate target let the block attach, and the merged text keeps the include
// lines instead of the fragments.
func TestMergeDocumentationMakefileFollowsTrackedIncludes(t *testing.T) {
	s := includeRepo(t, map[string]string{
		"tools/Makefile.shared":   "help:\n\t@echo help\nlint:\n\t@true\ntest:\n\t@true\n",
		"tools/tool-versions.env": "NAME=value\n",
	}, "tools/Makefile.shared", "tools/tool-versions.env")
	makefile := "include tools/Makefile.shared\ninclude tools/tool-versions.env\n\n.PHONY: verify-all\nverify-all: lint test\n"
	merged, err := mergeWithIncludes(s, makefile)
	if err != nil {
		t.Fatalf("tracked includes refused: %v", err)
	}
	if !strings.HasPrefix(merged, makefile) || !strings.Contains(merged, DocumentationMakefileBlock()) || strings.Contains(merged, "NAME=value") {
		t.Fatalf("merged text wrong:\n%s", merged)
	}
	if _, err := mergeDocumentationMakefile(makefile, false); err == nil {
		t.Fatal("without an expander the include must stay ambiguous")
	}
}

func TestMergeDocumentationMakefileTrackedFragmentDefiningGateRefuses(t *testing.T) {
	for _, target := range []string{"docs-lint", "docs-figures"} {
		s := includeRepo(t, map[string]string{"docs.mk": target + ":\n\t@echo fragment\n"}, "docs.mk")
		if _, err := mergeWithIncludes(s, "include docs.mk\n"); err == nil {
			t.Fatalf("fragment defining %s accepted", target)
		}
	}
}

func TestMergeDocumentationMakefileUnvouchedIncludesRefuse(t *testing.T) {
	s := includeRepo(t, map[string]string{
		"tracked.mk": "a:\n\t@true\n", "untracked.mk": "a:\n\t@true\n", "nested/up.mk": "include ../tracked.mk\n",
	}, "tracked.mk", "nested/up.mk")
	if err := os.Symlink("tracked.mk", filepath.Join(s.repoPath, "link.mk")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	testsupport.RunFixtureGit(t, s.repoPath, []string{"add", "--", "link.mk"})
	for name, makefile := range map[string]string{
		"untracked":        "include untracked.mk\n",
		"tracked symlink":  "include link.mk\n",
		"missing":          "include missing.mk\n",
		"soft include":     "-include tracked.mk\n",
		"sinclude":         "sinclude tracked.mk\n",
		"outside":          "include ../tracked.mk\n",
		"absolute":         "include " + filepath.ToSlash(filepath.Join(s.repoPath, "tracked.mk")) + "\n",
		"variable":         "include $(DIR)/tracked.mk\n",
		"wildcard":         "include *.mk\n",
		"function":         "include $(wildcard tracked.mk)\n",
		"directory":        "include nested\n",
		"nested outside":   "include nested/up.mk\n",
		"one tracked, one": "include tracked.mk untracked.mk\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeWithIncludes(s, makefile); err == nil {
				t.Fatal("unvouched include accepted")
			}
		})
	}
}

func TestMergeDocumentationMakefileNestedIncludeDepthBound(t *testing.T) {
	files := map[string]string{"leaf.mk": "lint:\n\t@true\n"}
	tracked := []string{"leaf.mk"}
	// f1 includes f2 ... the last fragment includes the leaf.
	build := func(length int) *adoptSession {
		for index := 1; index <= length; index++ {
			next := "leaf.mk"
			if index < length {
				next = "f" + string(rune('0'+index+1)) + ".mk"
			}
			name := "f" + string(rune('0'+index)) + ".mk"
			files[name] = "include " + next + "\n"
			tracked = append(tracked, name)
		}
		return includeRepo(t, files, tracked...)
	}
	if _, err := mergeWithIncludes(build(3), "include f1.mk\n"); err != nil {
		t.Fatalf("leaf at the depth bound refused: %v", err)
	}
	if _, err := mergeWithIncludes(build(4), "include f1.mk\n"); err == nil {
		t.Fatal("leaf one level over the depth bound accepted")
	}
}

func TestReadTrackedFragmentBoundaries(t *testing.T) {
	s := includeRepo(t, map[string]string{"a.mk": "x:\n", "b.mk": "y:\n"}, "a.mk")
	ctx := context.Background()
	if text, err := readTrackedFragment(ctx, s.repoPath, "./a.mk"); err != nil || text != "x:\n" {
		t.Fatalf("tracked fragment = %q, %v", text, err)
	}
	causes := map[string]string{
		"b.mk": "not tracked", "": "", ".": "", "../a.mk": "leaves the repository", "a.mk/../b.mk": "not tracked",
	}
	for rel, cause := range causes {
		_, err := readTrackedFragment(ctx, s.repoPath, rel)
		if err == nil || !strings.Contains(err.Error(), cause) {
			t.Fatalf("%q: error %v, want cause %q", rel, err, cause)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := readTrackedFragment(cancelled, s.repoPath, "a.mk"); err == nil {
		t.Fatal("cancelled context read the fragment")
	}
}

// TestIncludeExpanderNamesPathAndCause: an include that is not followed says which one and why.
func TestIncludeExpanderNamesPathAndCause(t *testing.T) {
	s := includeRepo(t, map[string]string{"free.mk": "a:\n"})
	_, notes := s.includeExpander(context.Background())("include free.mk\n")
	if len(notes) != 1 || !strings.Contains(notes[0], "free.mk") || !strings.Contains(notes[0], "not tracked") {
		t.Fatalf("notes %q", notes)
	}
	_, err := mergeWithIncludes(s, "include free.mk\n")
	if err == nil || !strings.Contains(err.Error(), "include free.mk not followed") {
		t.Fatalf("refusal does not name the include: %v", err)
	}
}

func TestMakefileImplicitSourceNearBoundary(t *testing.T) {
	big := map[string]string{"gen.mk": "a:\n"}
	for i := 0; i <= maxImplicitSourceEntries; i++ {
		big[fmt.Sprintf("d/f%d", i)] = ""
	}
	s := includeRepo(t, map[string]string{"gen.mk": "a:\n"}, "gen.mk")
	if err := makefileImplicitSourceNear(context.Background(), s.repoPath, "gen.mk"); err != nil {
		t.Fatalf("lone file: %v", err)
	}
	for _, name := range []string{"xgen.mk", "gen.mkx", "gen.mk.sh", "s.gen.mk.sh", "RCS", "SCCS"} {
		mustWrite(t, filepath.Join(s.repoPath, name), "")
		if err := makefileImplicitSourceNear(context.Background(), s.repoPath, "gen.mk"); err == nil {
			t.Errorf("neighbour %s not seen", name)
		}
		if err := os.Remove(filepath.Join(s.repoPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := makefileImplicitSourceNear(context.Background(), s.repoPath, "missing/gen.mk"); err == nil {
		t.Error("unreadable directory counted as clean")
	}
	over := includeRepo(t, big, "gen.mk")
	if err := makefileImplicitSourceNear(context.Background(), over.repoPath, "d/x.mk"); err == nil {
		t.Error("directory over the entry bound counted as clean")
	}
}

// remadeCase is a Makefile that includes the tracked gen.mk (text "tracked") while something makes
// Make rebuild gen.mk before reading it (text "remade" or a failed build): the reader must refuse.
type remadeCase struct {
	name, makefile string
	files          map[string]string // beside gen.mk and the Makefile; every one is newer than gen.mk
}

const (
	trackedGenMk = "docs-lint:\n\t@echo tracked\n"
	remadeGenMk  = "docs-lint:\n\t@echo remade\n"
)

// remadeCases are measured against GNU Make 4.4.1 (TestRemadeIncludeCasesMatchGNUMake replays them
// when GNU Make is installed): each rebuilds gen.mk, so Make runs "remade" (or fails running the
// missing SCCS tool "get") instead of the tracked recipe. An allow-list reader follows none.
var remadeCases = []remadeCase{
	{"sibling gen.mk.sh", "include gen.mk\n", map[string]string{"gen.mk.sh": remadeGenMk}},
	{"vpath directive", "vpath %.sh src\ninclude gen.mk\n", map[string]string{"src/gen.mk.sh": remadeGenMk}},
	{"VPATH variable", "VPATH = src\ninclude gen.mk\n", map[string]string{"src/gen.mk.sh": remadeGenMk}},
	{"chained SCCS", "VPATH = src\ninclude gen.mk\n", map[string]string{"src/s.gen.mk.sh": remadeGenMk}},
	{"dot-dot-slash target", "include gen.mk\n././gen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"dot-double-slash target", "include gen.mk\n.//gen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"plain target", "include gen.mk\ngen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"dot operand", "include ././gen.mk\ngen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"pattern rule", "include gen.mk\n%.mk: %.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"double-colon rule", "include gen.mk\ngen.mk:: gen.src\n\tcp $< $@\n", map[string]string{"gen.src": remadeGenMk}},
	{"custom suffix rule", ".SUFFIXES: .in .mk\n.in.mk:\n\tcp $< $@\ninclude gen.mk\n", map[string]string{"gen.in": remadeGenMk}},
}

// files2 is every file of the case including the tracked gen.mk.
func (tc remadeCase) files2() map[string]string {
	files := map[string]string{"gen.mk": trackedGenMk}
	for name, body := range tc.files {
		files[name] = body
	}
	return files
}

func TestMergeDocumentationMakefileRemadeIncludeRefuses(t *testing.T) {
	for _, tc := range remadeCases {
		s := includeRepo(t, tc.files2(), "gen.mk")
		_, err := mergeWithIncludes(s, tc.makefile)
		if err == nil {
			t.Errorf("%s: remade include followed", tc.name)
		}
	}
}

// TestRemadeIncludeCasesMatchGNUMake replays remadeCases (and the safe controls) against GNU Make:
// a refused case must not run the tracked recipe, an allowed one must.
func TestRemadeIncludeCasesMatchGNUMake(t *testing.T) {
	gnuMake := testsupport.GNUMake(t)
	testsupport.RequireGNUMakeShell(t, "cat", "chmod", "cp")
	run := func(tc remadeCase) string {
		dir := t.TempDir()
		for name, body := range tc.files2() {
			mustWrite(t, filepath.Join(dir, filepath.FromSlash(name)), body)
		}
		mustWrite(t, filepath.Join(dir, "Makefile"), tc.makefile)
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(dir, "gen.mk"), old, old); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gnuMake, "-s", "docs-lint")
		cmd.Dir, cmd.Env = dir, append(os.Environ(), "LC_ALL=C", "MAKEFLAGS=")
		out, err := cmd.CombinedOutput()
		var exited *exec.ExitError
		if err != nil && !errors.As(err, &exited) { // a failed rebuild exits non-zero by design
			t.Fatalf("running %s: %v", gnuMake, err)
		}
		return string(out)
	}
	for _, tc := range remadeCases {
		if out := run(tc); strings.Contains(out, "tracked") {
			t.Errorf("%s: Make read the tracked text, so the case proves nothing: %q", tc.name, out)
		}
	}
	safe := remadeCase{"no source", "include gen.mk\nother: x\n\t@true\n", nil}
	if out := run(safe); !strings.Contains(out, "tracked") {
		t.Errorf("safe control: Make did not read the tracked text: %q", out)
	}
}

// TestMergeDocumentationMakefileAllowListFollowsSafeIncludes: the includes the allow-list keeps
// following (GNU Make reads the tracked text in each; TestRemadeIncludeCasesMatchGNUMake holds the
// plain case).
func TestMergeDocumentationMakefileAllowListFollowsSafeIncludes(t *testing.T) {
	for name, makefile := range map[string]string{
		"plain":                 "include gen.mk\n",
		"dot operand":           "include ./gen.mk\n",
		"rule for another file": "include gen.mk\nother: x\n\t@true\n",
		"percent in recipe":     "include gen.mk\nother:\n\t@printf '%s: x\\n' y\n",
		"percent in prereq":     "include gen.mk\nobjs := $(SRC:%.c=%.o)\nother: $(objs)\n",
		"assignment with colon": "include gen.mk\nT := a::b\n",
	} {
		s := includeRepo(t, map[string]string{"gen.mk": "help:\n", "other.txt": "x\n"}, "gen.mk")
		if _, err := mergeWithIncludes(s, makefile); err != nil {
			t.Errorf("%s: safe include refused: %v", name, err)
		}
	}
}

// TestReconcileMakefileFollowsTrackedIncludeThroughSession drives the production entry point
// (reconcileMakefile, the adopt path) so a regression in the wiring of the expander is caught.
func TestReconcileMakefileFollowsTrackedIncludeThroughSession(t *testing.T) {
	makefile := "include shared.mk\n\n.PHONY: verify-all\nverify-all: lint\n"
	s := includeDocsSession(t, map[string]string{makefileName: makefile, "shared.mk": "lint:\n\t@true\n"}, AdoptOptions{})
	if err := reconcileMakefile(t.Context(), s); err != nil {
		t.Fatalf("tracked include refused through the session: %v", err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, makefileName)); !strings.Contains(got, DocumentationMakefileBlock()) || !strings.HasPrefix(got, makefile) {
		t.Fatalf("block not attached:\n%s", got)
	}
	bad := includeDocsSession(t, map[string]string{makefileName: makefile, "shared.mk": "docs-lint:\n\t@true\n"}, AdoptOptions{})
	if err := reconcileMakefile(t.Context(), bad); err == nil {
		t.Fatal("fragment defining docs-lint accepted through the session")
	}
	untracked := includeDocsSession(t, map[string]string{makefileName: makefile}, AdoptOptions{})
	mustWrite(t, filepath.Join(untracked.repoPath, "shared.mk"), "lint:\n")
	if err := reconcileMakefile(t.Context(), untracked); err == nil {
		t.Fatal("untracked include accepted through the session")
	}
}

// includeDocsSession is docsSession with a declared verification plan, which reconcileMakefile reads.
func includeDocsSession(t *testing.T, files map[string]string, opts AdoptOptions) *adoptSession {
	t.Helper()
	s := docsSession(t, files, opts)
	s.verification = &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"go", "build", "./..."}}, Test: [][]string{{"go", "test", "./..."}}}
	return s
}
