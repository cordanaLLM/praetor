package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// declineManifest is a manifest whose adoption.decline lists names.
func declineManifest(names ...string) *config.Manifest {
	return &config.Manifest{Adoption: &config.AdoptionPolicy{Decline: names}}
}

// validPersona is a persona whose front matter declares every required key.
func validPersona(name string) string {
	return "---\nname: " + name + "\ndescription: \"Fixture persona " + name + ".\"\nmodel: flash\n---\n\n# " + name + "\n"
}

// runAgentDefinitions runs the gate against root and returns what it printed and its error.
func runAgentDefinitions(t *testing.T, manifest *config.Manifest, root string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return auditAgentDefinitions(t.Context(), manifest, root) })
}

// Positive: flat personas are counted, the count compile-context projects; a repository
// without .agents/agents passes silently, as before.
func TestAuditAgentDefinitionsCountsFlatPersonas(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".agents/agents/repo-auditor.md", validPersona("repo-auditor"))
	writeFixtureFile(t, root, ".agents/agents/repo-gatekeeper.md", validPersona("repo-gatekeeper"))
	out, err := runAgentDefinitions(t, nil, root)
	if err != nil || !strings.Contains(out, "[PASS] Agent definitions verified (2 agents registered).") {
		t.Fatalf("flat personas: out %q, err %v", out, err)
	}
	out, err = runAgentDefinitions(t, nil, t.TempDir())
	if err != nil || out != "" {
		t.Fatalf("absent directory: out %q, err %v; want silence", out, err)
	}
}

// Negative: one directory per agent is named with what each holds and the layout praetor
// reads, never reported as zero definitions; a flat persona beside it does not hide it; a
// directory holding only agent.json is reported, not counted.
func TestAuditAgentDefinitionsNamesDirectoryAgents(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"repo-auditor", "repo-gatekeeper"} {
		writeFixtureFile(t, root, ".agents/agents/"+name+"/AGENTS.md", "# "+name+"\n")
		writeFixtureFile(t, root, ".agents/agents/"+name+"/agent.json", "{}\n")
	}
	_, err := runAgentDefinitions(t, nil, root)
	mustErrContain(t, err, "repo-auditor/ (holds AGENTS.md), repo-gatekeeper/ (holds AGENTS.md)")
	mustErrContain(t, err, "praetor reads one persona file per agent, .agents/agents/<name>.md")
	if strings.Contains(err.Error(), "zero agent definitions") {
		t.Fatalf("directory agents were reported as zero definitions: %v", err)
	}

	writeFixtureFile(t, root, ".agents/agents/reviewer.md", "# reviewer\n")
	writeFixtureFile(t, root, ".agents/agents/configured/agent.json", "{}\n")
	_, err = runAgentDefinitions(t, nil, root)
	mustErrContain(t, err, "does not project (3): configured/ (holds agent.json but no AGENTS.md), repo-auditor/")
}

// Negative: a directory with neither persona files nor agent directories still fails.
func TestAuditAgentDefinitionsEmptyDirectoryFails(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".agents/agents/.gitkeep", "")
	_, err := runAgentDefinitions(t, nil, root)
	mustErrContain(t, err, ".agents/agents directory exists but contains zero agent definitions")
}

// Boundary: an agent-definitions decline is honoured like the readme decline, and the report
// still says what the directory holds; an invalid decline list fails closed; a long list of
// unprojected entries is bounded.
func TestAuditAgentDefinitionsDeclineAndBounds(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".agents/agents/reviewer.md", "# reviewer\n")
	writeFixtureFile(t, root, ".agents/agents/repo-auditor/AGENTS.md", "# auditor\n")
	out, err := runAgentDefinitions(t, declineManifest("agent-definitions"), root)
	want := "[INFO] Agent definitions declined by adoption.decline; .agents/agents holds 1 persona file(s) " +
		"compile-context projects and entries it does not project (1): repo-auditor/ (holds AGENTS.md)."
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("declined: out %q, err %v; want %q", out, err, want)
	}
	out, err = runAgentDefinitions(t, declineManifest("agent-definitions"), t.TempDir())
	if err != nil || !strings.Contains(out, "[INFO] Agent definitions declined by adoption.decline.\n") {
		t.Fatalf("declined, absent directory: out %q, err %v", out, err)
	}
	_, err = runAgentDefinitions(t, declineManifest("agent-definitoins"), root)
	mustErrContain(t, err, "Resolve agent-definitions adoption decline")

	crowded := t.TempDir()
	for i := 0; i < maxReportedAgentEntries+2; i++ {
		writeFixtureFile(t, crowded, fmt.Sprintf(".agents/agents/agent-%02d/AGENTS.md", i), "# agent\n")
	}
	_, err = runAgentDefinitions(t, nil, crowded)
	mustErrContain(t, err, "agent-09/ (holds AGENTS.md), and 2 more;")
}

// Negative (#1280): a persona whose front matter does not parse, one without front matter and
// one lacking description each fail the audit, naming the file and what is wrong; a valid
// persona beside them does not hide them.
func TestAuditAgentDefinitionsDecodesFrontMatter(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".agents/agents/good.md", validPersona("good"))
	writeFixtureFile(t, root, ".agents/agents/broken.md", "---\nname: broken\ndescription: [unclosed\n---\n\n# broken\n")
	writeFixtureFile(t, root, ".agents/agents/bare.md", "# bare\n")
	writeFixtureFile(t, root, ".agents/agents/nameonly.md", "---\nname: nameonly\n---\n\n# nameonly\n")
	out, err := runAgentDefinitions(t, nil, root)
	mustErrContain(t, err, ".agents/agents holds personas whose front matter does not decode (3)")
	mustErrContain(t, err, ".agents/agents/broken.md (front matter is not valid YAML")
	mustErrContain(t, err, ".agents/agents/bare.md (opens with no front matter")
	mustErrContain(t, err, ".agents/agents/nameonly.md (front matter lacks the required key(s) description")
	if strings.Contains(err.Error(), "good.md") || strings.Contains(out, "[PASS]") {
		t.Fatalf("the valid persona was reported, or the gate passed: out %q, err %v", out, err)
	}
}

// Boundary (#1280): a front matter that never closes fails; a key present but empty, or not a
// string, counts as missing; both required keys missing are named together; a decline still
// skips the decoding.
func TestAuditAgentDefinitionsFrontMatterBoundaries(t *testing.T) {
	cases := map[string]struct{ persona, want string }{
		"unclosed":      {"---\nname: open\ndescription: never closed\n", "opens a front matter block that does not close"},
		"empty value":   {"---\nname: x\ndescription: \"  \"\nmodel: flash\n---\n", "lacks the required key(s) description"},
		"not a string":  {"---\nname: [a, b]\ndescription: d\nmodel: flash\n---\n", "lacks the required key(s) name"},
		"both missing":  {"---\nmainAgent: true\n---\n", "lacks the required key(s) name, description"},
		"empty block":   {"---\n---\n# empty\n", "lacks the required key(s) name, description"},
		"missing model": {"---\nname: x\ndescription: d\n---\n", "lacks the required key \"model\""},
		"invalid model": {"---\nname: x\ndescription: d\nmodel: sonnet\n---\n", "front matter model \"sonnet\" is invalid"},
		"inherit model": {"---\nname: x\ndescription: d\nmodel: inherit\n---\n", "front matter model \"inherit\" is invalid"},
	}
	for name, tc := range cases {
		root := t.TempDir()
		writeFixtureFile(t, root, ".agents/agents/p.md", tc.persona)
		_, err := runAgentDefinitions(t, nil, root)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to contain %q", name, err, tc.want)
		}
	}
	root := t.TempDir()
	writeFixtureFile(t, root, ".agents/agents/p.md", "# no front matter\n")
	if _, err := runAgentDefinitions(t, declineManifest("agent-definitions"), root); err != nil {
		t.Fatalf("a declined gate decoded the personas: %v", err)
	}
}
