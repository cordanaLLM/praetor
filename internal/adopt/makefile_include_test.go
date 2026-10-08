package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// includeRepo builds a git repository holding files, tracks the names in tracked, and returns a
// session over it. The Makefile is only text the tests pass to the merge.
func includeRepo(t *testing.T, files map[string]string, tracked ...string) *adoptSession {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if len(tracked) > 0 {
		run(append([]string{"add", "--"}, tracked...)...)
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
	gitAdd := exec.CommandContext(context.Background(), "git", "add", "--", "link.mk")
	gitAdd.Dir = s.repoPath
	if out, err := gitAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
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
	if text, ok := readTrackedFragment(ctx, s.repoPath, "./a.mk"); !ok || text != "x:\n" {
		t.Fatalf("tracked fragment = %q, %v", text, ok)
	}
	for _, rel := range []string{"b.mk", "", ".", "../a.mk", "a.mk/../b.mk"} {
		if _, ok := readTrackedFragment(ctx, s.repoPath, rel); ok {
			t.Fatalf("%q read", rel)
		}
	}
}

// TestMergeDocumentationMakefileRemadeIncludeRefuses covers an include Make remakes before it
// reads it (measured against GNU Make 4.4.1): a built-in rule builds gen.mk from a neighbouring
// source, so the tracked text is not what Make reads and the include stays ambiguous.
func TestMergeDocumentationMakefileRemadeIncludeRefuses(t *testing.T) {
	neighbours := []string{"gen.mk.sh", "gen.mk.c", "gen.mk,v", "s.gen.mk", "RCS/gen.mk,v", "SCCS/s.gen.mk"}
	for _, neighbour := range neighbours {
		s := includeRepo(t, map[string]string{"gen.mk": "help:\n", neighbour: "x\n"}, "gen.mk")
		if _, err := mergeWithIncludes(s, "include gen.mk\n"); err == nil {
			t.Errorf("include with neighbour %s followed", neighbour)
		}
	}
	s := includeRepo(t, map[string]string{"gen.mk": "help:\n", "gen.mk.in": "docs-lint:\n"}, "gen.mk")
	if _, err := mergeWithIncludes(s, "include gen.mk\ngen.mk: gen.mk.in\n\tcp $< $@\n"); err == nil {
		t.Error("include with an explicit rule followed")
	}
	s = includeRepo(t, map[string]string{"gen.mk": "help:\n", "other.txt": "x\n"}, "gen.mk")
	if _, err := mergeWithIncludes(s, "include gen.mk\n"); err != nil {
		t.Errorf("fragment with no source beside it refused: %v", err)
	}
}

// TestMergeDocumentationMakefileRemadeIncludeSpellings covers the spellings Make treats alike
// (GNU Make 4.4.1 strips a leading "./" from include operands and targets) and the suffix rules
// that build an include from a neighbour of another suffix: each stays ambiguous and refuses.
func TestMergeDocumentationMakefileRemadeIncludeSpellings(t *testing.T) {
	cases := []struct {
		name, makefile string
		files          map[string]string
	}{
		{"dot operand, plain target", "include ./gen.mk\ngen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.mk": "help:\n"}},
		{"plain operand, dot target", "include gen.mk\n./gen.mk: gen.src\n\tcp $< $@\n", map[string]string{"gen.mk": "help:\n"}},
		{"custom suffix rule", ".SUFFIXES: .in .mk\n.in.mk:\n\tcp $< $@\ninclude gen.mk\n", map[string]string{"gen.mk": "help:\n", "gen.in": "x\n"}},
		{"built-in suffix rule", "include gen.s\n", map[string]string{"gen.s": "help:\n", "gen.S": "x\n"}},
		{"built-in suffix without neighbour", "include gen.s\n", map[string]string{"gen.s": "help:\n"}},
	}
	for _, tc := range cases {
		tracked := "gen.mk"
		if _, ok := tc.files["gen.s"]; ok {
			tracked = "gen.s"
		}
		s := includeRepo(t, tc.files, tracked)
		if _, err := mergeWithIncludes(s, tc.makefile); err == nil {
			t.Errorf("%s: include followed", tc.name)
		}
	}
	s := includeRepo(t, map[string]string{"gen.mk": "help:\n"}, "gen.mk")
	if _, err := mergeWithIncludes(s, "include ./gen.mk\n"); err != nil {
		t.Errorf("dot-prefixed literal include with no rule refused: %v", err)
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
