package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkInRootDir makes rel below repoPath a relative symlink to a real, empty directory target,
// also below repoPath, and returns the real directory. util.ConfinePath accepts such a link,
// which is why adoption used to write through it.
func linkInRootDir(t *testing.T, repoPath, rel, target string) string {
	t.Helper()
	real := filepath.Join(repoPath, filepath.FromSlash(target))
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repoPath, filepath.FromSlash(rel))
	relTarget, err := filepath.Rel(filepath.Dir(link), real)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return real
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("adoption wrote %d entries behind the link into %s: %v", len(entries), dir, entries)
	}
}

// Negative: a .agents or .github that is an in-root symlink to a real directory fails adoption
// before its first write. Adoption used to write the manifest, the vendor files, the pull request
// template and the documentation workflow through the link and fail only at agent-definitions,
// leaving a half-adopted repository. A dry run refuses it too, so its preview does not claim a
// run that would fail.
func TestAdopt_Negative_SymlinkedAgentSurfaceRefusedBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name, link string
		dryRun     bool
	}{
		{"agents", ".agents", false},
		{"github", ".github", false},
		{"agents-dry-run", ".agents", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := newTestRepo(t, "linked-"+tc.name)
			real := linkInRootDir(t, repoPath, tc.link, "shared/"+tc.name)
			before := snapshotTree(t, repoPath)

			rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: tc.dryRun})
			if err == nil {
				t.Fatalf("adoption accepted a symlinked %s", tc.link)
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Errorf("error %q does not name the symlink", err)
			}
			if rep == nil || len(rep.Errors) == 0 {
				t.Fatalf("the refusal is not in the report: %+v", rep)
			}
			if len(rep.CreatedFiles) != 0 || len(rep.ReconciledFiles) != 0 {
				t.Errorf("adoption reported writes before the refusal: created %v, reconciled %v", rep.CreatedFiles, rep.ReconciledFiles)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
			assertDirEmpty(t, real)
			for _, rel := range []string{manifestFile, agentsFile, "CLAUDE.md", ".github/copilot-instructions.md", prTemplateFile, DocumentationWorkflowFile} {
				if _, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s written before the refusal (lstat err=%v)", rel, err)
				}
			}
		})
	}
}

// Positive: a repository without links adopts as before, with every vendor file, canonical
// persona and persona copy written as a regular file.
func TestAdopt_Positive_PlainAgentSurfacesStillWritten(t *testing.T) {
	repoPath := newTestRepo(t, "plain-agent-surfaces")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	for _, rel := range []string{"CLAUDE.md", ".github/copilot-instructions.md", ".cursor/rules/hiss-invariants.mdc",
		auditorAgentFile, gatekeeperFile, ".claude/agents/repo-auditor.md", ".github/agents/repo-gatekeeper.md"} {
		info, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s: want a regular file, lstat err=%v", rel, err)
			continue
		}
		if !contains(rep.CreatedFiles, rel) {
			t.Errorf("%s written but not reported created: %v", rel, rep.CreatedFiles)
		}
	}
}

// Boundary: an in-root link to a real directory that no selected agent surface passes through is
// left alone. .gemini holds only the Gemini files, which agent_clients: [claude] leaves out, so
// adoption succeeds and writes nothing behind the link.
func TestAdopt_Boundary_UnselectedLinkedAgentDirIsLeftAlone(t *testing.T) {
	repoPath := newTestRepo(t, "unselected-linked-gemini")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nagent_clients: [claude]\n")
	real := linkInRootDir(t, repoPath, ".gemini", "shared/gemini")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	assertDirEmpty(t, real)
	if !fileExists(filepath.Join(repoPath, ".claude", "agents", "repo-auditor.md")) {
		t.Error("selected persona copy not written")
	}
}

// Boundary: the preflight checks only the steps that run. With agent-harness and
// agent-definitions declined, a symlinked .agents is nothing adoption writes to, so adoption
// succeeds and writes nothing behind it.
func TestAdopt_Boundary_DeclinedAgentStepsAreNotPreflighted(t *testing.T) {
	repoPath := newTestRepo(t, "declined-agent-steps")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [agent-harness, agent-definitions]\n")
	real := linkInRootDir(t, repoPath, ".agents", "shared/agents")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	assertDirEmpty(t, real)
}

// Negative: the vendor files and the canonical personas go through the root-pinned writer, so a
// link planted after the preflight is refused at write time too. The steps are called directly,
// past the preflight, and nothing is written behind the link.
func TestAdoptAgentWritesRefuseLinkPlantedAfterPreflight(t *testing.T) {
	t.Run("vendor", func(t *testing.T) {
		repoPath := t.TempDir()
		real := linkInRootDir(t, repoPath, ".cursor", "shared/cursor")
		s := &adoptSession{repoPath: repoPath, report: &AdoptReport{}}
		harness, err := buildAgentHarness("fixture", "framework", registerTestPlan())
		if err != nil {
			t.Fatal(err)
		}
		if err := transpileAgentTargets(context.Background(), s, harness, nil); err == nil {
			t.Fatal("vendor write followed a symlinked .cursor")
		}
		assertDirEmpty(t, real)
		// Every vendor file is checked before the first is written, so CLAUDE.md, which
		// compiles first, is not written either.
		if _, err := os.Lstat(filepath.Join(repoPath, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("CLAUDE.md written before the refused .cursor target (lstat err=%v)", err)
		}
	})
	t.Run("persona", func(t *testing.T) {
		repoPath := t.TempDir()
		real := linkInRootDir(t, repoPath, ".agents", "shared/agents")
		s := &adoptSession{repoPath: repoPath, report: &AdoptReport{}}
		if err := reconcileAgentDefinitions(context.Background(), s); err == nil {
			t.Fatal("persona write followed a symlinked .agents")
		}
		assertDirEmpty(t, real)
	})
}
