package compiler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// planPersonaRoot is a root holding one canonical persona with content.
func planPersonaRoot(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".agents", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planner.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// Positive: PlanAgents returns the copies CompileAgents writes, with the same targets and
// content, and writes none of them.
func TestPlanAgents_Positive_ListsCopiesWithoutWriting(t *testing.T) {
	const content = "---\nname: planner\n---\n# Planner\n"
	root := planPersonaRoot(t, content)
	planned, err := PlanAgents(t.Context(), root)
	if err != nil {
		t.Fatalf("PlanAgents: %v", err)
	}
	if len(planned) != 4 {
		t.Fatalf("planned %d copies, want 4: %+v", len(planned), planned)
	}
	for _, file := range planned {
		if file.Content != content {
			t.Errorf("%s content = %q, want the canonical persona", file.VendorTarget, file.Content)
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.VendorTarget))); !os.IsNotExist(err) {
			t.Errorf("PlanAgents wrote %s (lstat err=%v)", file.VendorTarget, err)
		}
	}
	written, err := CompileAgents(t.Context(), root)
	if err != nil {
		t.Fatalf("CompileAgents: %v", err)
	}
	for i := range written {
		if written[i].VendorTarget != planned[i].VendorTarget || written[i].Content != planned[i].Content {
			t.Fatalf("copy %d written %+v, planned %+v", i, written[i], planned[i])
		}
	}
}

// Negative: PlanAgents refuses what CompileAgents refuses before writing, a cancelled or absent
// context and a symlinked persona directory.
func TestPlanAgents_Negative_RefusesLikeCompileAgents(t *testing.T) {
	root := planPersonaRoot(t, "# Planner\n")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PlanAgents(cancelled, root); err == nil {
		t.Fatal("cancelled context accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".claude", "agents")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := PlanAgents(t.Context(), root); err == nil {
		t.Fatal("symlinked persona directory accepted")
	}
}

// Boundary: a root without canonical personas plans nothing, without error.
func TestPlanAgents_Boundary_NoPersonasPlansNothing(t *testing.T) {
	planned, err := PlanAgents(t.Context(), t.TempDir())
	if err != nil || len(planned) != 0 {
		t.Fatalf("planned %+v, err %v; want none", planned, err)
	}
}
