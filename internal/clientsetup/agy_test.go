package clientsetup

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The fixture is the redacted shape of a live Antigravity mcp_config.json
// (agy 1.2.5): one mcpServers map, stdio entries with optional env, and a
// remote entry spelled serverUrl. Names, paths and values are synthetic.
func agyLiveShape(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "agy-mcp_config.live-shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func agyServers(t *testing.T, content []byte) map[string]jsontext.Value {
	t.Helper()
	var root map[string]jsontext.Value
	if err := json.Unmarshal(content, &root); err != nil {
		t.Fatal(err)
	}
	var servers map[string]jsontext.Value
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestAGYMergeKeepsLiveShape(t *testing.T) {
	existing := agyLiveShape(t)
	p, err := BuildPlan(t.Context(), testRegistry(), AGY, existing)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "merge" || p.RelativePath != ".agents/mcp_config.json" || p.ExportName != "agy-mcp_config.json" || len(p.Commands) != 0 || !p.Changed {
		t.Fatalf("invalid merge plan: %+v", p)
	}
	servers := agyServers(t, p.Content)
	if len(servers) != 5 {
		t.Fatalf("server count changed: %d", len(servers))
	}
	for _, kept := range []string{`"serverUrl": "https://index.example.test/mcp"`, `"MEMORY_HARNESS": "redacted-harness"`, `"APPLICATION_CREDENTIALS": "/redacted/credentials.json"`, `"--prebuilt"`} {
		if !bytes.Contains(p.Content, []byte(kept)) {
			t.Fatalf("lost %s in %s", kept, p.Content)
		}
	}
	added, err := jsonObject(servers["praetor-dev"])
	if err != nil || len(added) != 2 || stringField(added, "command") != praetorMCP || !matchingArgs(added["args"], []string{"-transport=stdio"}) {
		t.Fatalf("added entry is not the native stdio shape: %s", servers["praetor-dev"])
	}
	again, err := BuildPlan(t.Context(), testRegistry(), AGY, p.Content)
	if err != nil || again.Changed || !bytes.Equal(again.Content, p.Content) {
		t.Fatalf("second run is not idempotent: %v %+v", err, again)
	}
}

func TestAGYMergeKeepsUnknownKeysAndServerOptions(t *testing.T) {
	existing := []byte(`{"futureSetting":{"number":9007199254740993},"mcpServers":{"praetor-dev":{"command":` + quoted(praetorMCP) + `,"args":["-transport=stdio"],"env":{"TOKEN":"sensitive-existing-value"},"disabled":true}}}`)
	registry := testRegistry()
	registry.Servers = append(registry.Servers, Server{Name: "second", Command: hostAbsolute("opt", "second"), Args: []string{}})
	p, err := BuildPlan(t.Context(), registry, AGY, existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"9007199254740993", "sensitive-existing-value", `"disabled": true`, `"second"`} {
		if !bytes.Contains(p.Content, []byte(kept)) {
			t.Fatalf("lost %s in %s", kept, p.Content)
		}
	}
	metadata, err := json.Marshal(p)
	if err != nil || bytes.Contains(metadata, []byte("sensitive-existing-value")) {
		t.Fatalf("candidate value reached metadata: %v", err)
	}
}

func TestAGYMergeRejectsConflictsAndAmbiguousInput(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      error
	}{
		{"same-name remote", `{"mcpServers":{"praetor-dev":{"serverUrl":"https://other.example.test/sse"}}}`, ErrConflict},
		// These three spell the registry's own command, so the case under test is the
		// conflict named in the row. With a POSIX literal on Windows the command differed
		// from the registry's, and "remote beside matching command" and "other args" both
		// collapsed into the "other command" branch: the table still passed while testing
		// something else (#135, HISS-20).
		{"remote beside matching command", `{"mcpServers":{"praetor-dev":{"command":` + quoted(praetorMCP) + `,"args":["-transport=stdio"],"serverUrl":"https://other.example.test/sse"}}}`, ErrConflict},
		{"other command", `{"mcpServers":{"praetor-dev":{"command":` + quoted(hostAbsolute("other")) + `,"args":["-transport=stdio"]}}}`, ErrConflict},
		{"other args", `{"mcpServers":{"praetor-dev":{"command":` + quoted(praetorMCP) + `,"args":[]}}}`, ErrConflict},
		{"JSONC line comment", "{ // operator note\n\"mcpServers\":{}}", nil},
		{"JSONC block comment", `{/* note */"mcpServers":{}}`, nil},
		{"trailing comma", `{"mcpServers":{},}`, nil},
		{"servers not an object", `{"mcpServers":[]}`, nil},
		{"duplicate server key", `{"mcpServers":{"a":{"command":"/a"},"a":{"command":"/b"}}}`, nil},
		{"array root", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			before := bytes.Clone(raw)
			p, err := BuildPlan(t.Context(), testRegistry(), AGY, raw)
			if err == nil || p != nil {
				t.Fatalf("accepted: %+v", p)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("wanted %v, got %v", tc.want, err)
			}
			if !bytes.Equal(raw, before) {
				t.Fatal("existing input mutated")
			}
		})
	}
}

func TestAGYMergeBoundaries(t *testing.T) {
	for _, existing := range [][]byte{nil, {}, []byte(`{}`), []byte(`{"mcpServers":{}}`)} {
		p, err := BuildPlan(t.Context(), testRegistry(), AGY, existing)
		if err != nil || !p.Changed || len(agyServers(t, p.Content)) != 1 {
			t.Fatalf("empty configuration %q: %v %+v", existing, err, p)
		}
	}
	if _, err := BuildPlan(t.Context(), testRegistry(), AGY, []byte(" \n")); err == nil {
		t.Fatal("whitespace-only configuration accepted as JSON")
	}
	full := Registry{Version: 1}
	for i := range MaxServers {
		full.Servers = append(full.Servers, Server{Name: fmt.Sprintf("server-%02d", i), Command: optServer, Args: []string{}})
	}
	p, err := BuildPlan(t.Context(), full, AGY, agyLiveShape(t))
	if err != nil || len(agyServers(t, p.Content)) != MaxServers+4 {
		t.Fatalf("full registry: %v", err)
	}
	again, err := BuildPlan(t.Context(), full, AGY, p.Content)
	if err != nil || again.Changed {
		t.Fatalf("full registry replay: %v", err)
	}
	full.Servers = append(full.Servers, Server{Name: "extra", Command: optServer})
	if _, err := BuildPlan(t.Context(), full, AGY, nil); err == nil {
		t.Fatal("registry above the server bound accepted")
	}
}
