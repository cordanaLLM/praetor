package router

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSyncPreservesClaudeSubagentSection(t *testing.T) {
	for _, section := range []string{
		"",
		"# Native task overrides\nclaude_subagent: {}\n",
		"# Native task overrides\nclaude_subagent:\n  implement: {model: sonnet, effort: high}\n",
	} {
		t.Run(section, func(t *testing.T) { checkSyncedClaudeSection(t, section) })
	}
}

func checkSyncedClaudeSection(t *testing.T, section string) {
	t.Helper()
	path := routingInput(t, routingFixture+section)
	expected := loadSynced(t, path)
	if _, err := syncCatalog(t.Context(), path, SyncOptions{}, stubDiscovery()); err != nil {
		t.Fatal(err)
	}
	actual := loadSynced(t, path)
	if !reflect.DeepEqual(expected.ClaudeSubagent, actual.ClaudeSubagent) {
		t.Fatalf("routes changed: %v -> %v", expected.ClaudeSubagent, actual.ClaudeSubagent)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if section != "" && !strings.Contains(string(first), "# Native task overrides") {
		t.Fatal("section comment lost")
	}
	if _, err := syncCatalog(t.Context(), path, SyncOptions{}, stubDiscovery()); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("sync is not stable")
	}
}
