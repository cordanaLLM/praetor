package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePersonaFixture builds a root with one canonical persona and, when manifest is set, a
// .standards.yaml beside it.
func writePersonaFixture(t *testing.T, manifest string) (root, src string) {
	t.Helper()
	root = t.TempDir()
	src = filepath.Join(root, ".agents", "agents")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "helper.md"), []byte("---\nname: helper\n---\n# Helper\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, src
}

func personaDirWritten(root, dir string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "helper.md"))
	return err == nil
}

// Positive: agent_clients in the manifest at the target root limits the persona copies to the
// selected clients' directories, reported as slash paths.
func TestCompileAgents_Positive_ManifestSelectsPersonaDirs(t *testing.T) {
	root, src := writePersonaFixture(t, "version: 1\nagent_clients: [codex, copilot]\n")
	files, err := CompileAgents(context.Background(), src, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.VendorTarget)
	}
	if want := ".github/agents/helper.md,.codex/agents/helper.md"; strings.Join(got, ",") != want {
		t.Fatalf("projected %v, want %s", got, want)
	}
	for _, dir := range []string{".claude/agents", ".gemini/agents"} {
		if personaDirWritten(root, dir) {
			t.Errorf("unselected persona directory %s written", dir)
		}
	}
	selected, excluded, err := SelectPersonaDirs(context.Background(), root)
	if err != nil || strings.Join(selected, ",") != ".github/agents,.codex/agents" ||
		strings.Join(excluded, ",") != ".claude/agents,.gemini/agents" {
		t.Fatalf("SelectPersonaDirs = %v, %v, %v", selected, excluded, err)
	}
}

// Negative: an unknown client fails before any persona is written, and a manifest that does
// not parse is not read as "every client".
func TestCompileAgents_Negative_InvalidSelectionWritesNothing(t *testing.T) {
	for _, manifest := range []string{"version: 1\nagent_clients: [claude, vim]\n", "version: 1\nagent_clients: [claude\n"} {
		root, src := writePersonaFixture(t, manifest)
		if _, err := CompileAgents(context.Background(), src, root); err == nil {
			t.Fatalf("CompileAgents accepted %q", manifest)
		}
		if personaDirWritten(root, ".claude/agents") {
			t.Errorf("%q: a rejected selection still wrote a persona", manifest)
		}
	}
}

// Boundary: no manifest keeps every client's directory (the behaviour before selection), and
// an empty list or one naming only clients without a persona directory writes none.
func TestCompileAgents_Boundary_AbsentEmptyAndPersonalessSelections(t *testing.T) {
	root, src := writePersonaFixture(t, "")
	files, err := CompileAgents(context.Background(), src, root)
	if err != nil || len(files) != 4 {
		t.Fatalf("no manifest: %d files, %v; want 4", len(files), err)
	}
	for _, manifest := range []string{"version: 1\nagent_clients: []\n", "version: 1\nagent_clients: [cursor, windsurf]\n"} {
		root, src := writePersonaFixture(t, manifest)
		files, err := CompileAgents(context.Background(), src, root)
		if err != nil || len(files) != 0 {
			t.Fatalf("%q: %d files, %v; want 0", manifest, len(files), err)
		}
		if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
			t.Errorf("%q: a persona directory was created: %v", manifest, err)
		}
	}
}
