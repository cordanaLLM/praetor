package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeAgentEntry writes content to rel below the canonical persona directory of root.
func writeAgentEntry(t *testing.T, root, rel, content string) {
	t.Helper()
	writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), filepath.FromSlash(rel)), content)
}

// Positive: the inventory counts the flat persona files compile-context projects, the same set
// listCanonicalAgents returns, and names every subdirectory with what it holds, so a directory
// agent is reported instead of silently skipped. A regular non-.md file holds no definition.
func TestReadAgentInventoryCountsPersonasAndNamesDirectories(t *testing.T) {
	root := t.TempDir()
	writeAgentEntry(t, root, "reviewer.md", "# reviewer\n")
	writeAgentEntry(t, root, "tester.md", "# tester\n")
	writeAgentEntry(t, root, ".gitkeep", "")
	writeAgentEntry(t, root, "auditor/AGENTS.md", "# auditor\n")
	writeAgentEntry(t, root, "auditor/agent.json", "{}\n")
	writeAgentEntry(t, root, "configured/agent.json", "{}\n")
	writeAgentEntry(t, root, "notes/readme.txt", "notes\n")

	inventory, err := ReadAgentInventory(t.Context(), root)
	if err != nil {
		t.Fatalf("ReadAgentInventory: %v", err)
	}
	want := AgentInventory{
		Present:  true,
		Personas: []string{"reviewer.md", "tester.md"},
		Unprojected: []UnprojectedAgentEntry{
			{Name: "auditor/", Detail: "holds AGENTS.md"},
			{Name: "configured/", Detail: "holds agent.json but no AGENTS.md"},
			{Name: "notes/", Detail: "holds no AGENTS.md"},
		},
	}
	if !reflect.DeepEqual(inventory, want) {
		t.Fatalf("inventory = %+v, want %+v", inventory, want)
	}
	listed, err := listCanonicalAgents(t.Context(), root)
	if err != nil || !reflect.DeepEqual(listed, inventory.Personas) {
		t.Fatalf("listCanonicalAgents = %v, %v; the inventory counted %v", listed, err, inventory.Personas)
	}
}

// Negative: an absent directory is an empty inventory, not an error; a regular file where the
// directory belongs, and a symlinked persona, are refused exactly as listCanonicalAgents
// refuses them, so audit never counts what compile-context would refuse to project.
func TestReadAgentInventoryRefusesWhatCompileContextRefuses(t *testing.T) {
	inventory, err := ReadAgentInventory(t.Context(), t.TempDir())
	if err != nil || inventory.Present || len(inventory.Personas) != 0 || len(inventory.Unprojected) != 0 {
		t.Fatalf("absent directory: inventory %+v, err %v; want an empty inventory", inventory, err)
	}

	misplaced := t.TempDir()
	writeOutputFixture(t, filepath.Join(misplaced, filepath.FromSlash(CanonicalAgentsRel)), "not a directory")
	if _, err := ReadAgentInventory(t.Context(), misplaced); err == nil {
		t.Fatal("a regular file at .agents/agents was read as a persona directory")
	}

	linked := t.TempDir()
	writeAgentEntry(t, linked, "real.md", "# real\n")
	symlinkOrSkip(t, filepath.Join(linked, filepath.FromSlash(CanonicalAgentsRel), "real.md"),
		filepath.Join(linked, filepath.FromSlash(CanonicalAgentsRel), "linked.md"))
	if _, err := ReadAgentInventory(t.Context(), linked); !errors.Is(err, errPersonaNotRegular) {
		t.Fatalf("symlinked persona: want errPersonaNotRegular, got %v", err)
	}
}

// Negative: a symlinked entry without the .md suffix, one pointing at an agent directory
// included, is reported as unprojected and never followed.
func TestReadAgentInventoryReportsSymlinkedDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeAgentEntry(t, root, "reviewer.md", "# reviewer\n")
	writeOutputFixture(t, filepath.Join(outside, "AGENTS.md"), "# outside\n")
	symlinkOrSkip(t, outside, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "linked"))

	inventory, err := ReadAgentInventory(t.Context(), root)
	if err != nil {
		t.Fatalf("ReadAgentInventory: %v", err)
	}
	want := []UnprojectedAgentEntry{{Name: "linked", Detail: "not a regular file or directory"}}
	if !reflect.DeepEqual(inventory.Unprojected, want) || !reflect.DeepEqual(inventory.Personas, []string{"reviewer.md"}) {
		t.Fatalf("inventory = %+v; want persona reviewer.md and unprojected %+v", inventory, want)
	}
}

// Boundary: a persona file and a directory of the same name are one persona and one reported
// entry, never two personas; a directory whose AGENTS.md is a symlink holds no AGENTS.md; an
// agent directory above the cap says so; a persona directory above the cap is refused.
func TestReadAgentInventoryBoundaries(t *testing.T) {
	root := t.TempDir()
	writeAgentEntry(t, root, "auditor.md", "# auditor\n")
	writeAgentEntry(t, root, "auditor/AGENTS.md", "# auditor\n")
	for i := 0; i <= maxAgentProjections; i++ {
		writeAgentEntry(t, root, fmt.Sprintf("crowded/file-%02d.txt", i), "x\n")
	}
	inventory, err := ReadAgentInventory(t.Context(), root)
	if err != nil {
		t.Fatalf("ReadAgentInventory: %v", err)
	}
	want := AgentInventory{
		Present:  true,
		Personas: []string{"auditor.md"},
		Unprojected: []UnprojectedAgentEntry{
			{Name: "auditor/", Detail: "holds AGENTS.md"},
			{Name: "crowded/", Detail: fmt.Sprintf("holds more than %d entries", maxAgentProjections)},
		},
	}
	if !reflect.DeepEqual(inventory, want) {
		t.Fatalf("inventory = %+v, want %+v", inventory, want)
	}

	full := t.TempDir()
	for i := 0; i <= maxAgentProjections; i++ {
		writeAgentEntry(t, full, fmt.Sprintf("persona-%02d.md", i), "# persona\n")
	}
	if _, err := ReadAgentInventory(t.Context(), full); err == nil {
		t.Fatalf("a persona directory above %d entries was read", maxAgentProjections)
	}
}

// Boundary: an AGENTS.md that is a symlink is not a definition the directory holds.
func TestReadAgentInventorySymlinkedAgentsFile(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeAgentEntry(t, root, "reviewer.md", "# reviewer\n")
	writeOutputFixture(t, filepath.Join(outside, "AGENTS.md"), "# outside\n")
	dir := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "linked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(outside, "AGENTS.md"), filepath.Join(dir, "AGENTS.md"))
	inventory, err := ReadAgentInventory(t.Context(), root)
	if err != nil {
		t.Fatalf("ReadAgentInventory: %v", err)
	}
	want := []UnprojectedAgentEntry{{Name: "linked/", Detail: "holds no AGENTS.md"}}
	if !reflect.DeepEqual(inventory.Unprojected, want) {
		t.Fatalf("unprojected = %+v, want %+v", inventory.Unprojected, want)
	}
}
