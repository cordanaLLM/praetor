package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testRegistry() Registry {
	return Registry{Version: 1, Servers: []Server{{Name: "praetor-dev", Command: "/opt/praetor/bin/praetor-mcp", Args: []string{"-transport=stdio"}}}}
}

func TestBuildPlanClientShapesAndReplay(t *testing.T) {
	for _, client := range []Client{Codex, Claude, Gemini, OpenCodeV1, Continue, Cline, Kilo, AGY} {
		t.Run(string(client), func(t *testing.T) {
			p, err := BuildPlan(t.Context(), testRegistry(), client, nil)
			if err != nil {
				t.Fatal(err)
			}
			if p.Client != client || !p.Changed || p.Documentation == "" || len(p.SourceSHA256) != 64 || len(p.RegistrySHA256) != 64 {
				t.Fatalf("invalid plan: %+v", p)
			}
			if client == Codex || client == AGY {
				if p.Mode != "native" || p.RelativePath != "" || len(p.Commands) != 1 {
					t.Fatalf("invalid native plan: %+v", p)
				}
				want := []string{"codex", "mcp", "add", "praetor-dev", "--", "/opt/praetor/bin/praetor-mcp", "-transport=stdio"}
				if client == AGY {
					want = []string{"agy", "mcp", "add", "--type", "stdio", "praetor-dev", "--", "/opt/praetor/bin/praetor-mcp", "-transport=stdio"}
				}
				if !slices.Equal(p.Commands[0], want) {
					t.Fatalf("argv differs: %v", p.Commands)
				}
				if client == Codex && string(p.Content) != "[mcp_servers.praetor-dev]\ncommand = \"/opt/praetor/bin/praetor-mcp\"\nargs = [\"-transport=stdio\"]\n\n" {
					t.Fatalf("bad TOML: %s", p.Content)
				}
				return
			}
			validateClientShape(t, p)
			again, err := BuildPlan(t.Context(), testRegistry(), client, p.Content)
			if err != nil || again.Changed || !bytes.Equal(p.Content, again.Content) {
				t.Fatalf("non-idempotent replay: %v %+v", err, again)
			}
		})
	}
}

func validateClientShape(t *testing.T, p *Plan) {
	t.Helper()
	if p.Client == Continue {
		var got struct {
			Name    string
			Version string
			Schema  string
			Servers []Server `yaml:"mcpServers"`
		}
		if err := yaml.Unmarshal(p.Content, &got); err != nil {
			t.Fatal(err)
		}
		if got.Schema != "v1" || got.Version == "" || got.Name == "" || len(got.Servers) != 1 || got.Servers[0].Command != testRegistry().Servers[0].Command {
			t.Fatalf("bad Continue document: %s", p.Content)
		}
		return
	}
	var got map[string]jsontext.Value
	if err := json.Unmarshal(p.Content, &got); err != nil {
		t.Fatal(err)
	}
	key := "mcpServers"
	if p.Client == OpenCodeV1 || p.Client == Kilo {
		key = "mcp"
	}
	var servers map[string]jsontext.Value
	if err := json.Unmarshal(got[key], &servers); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || !matchingJSONServer(servers["praetor-dev"], p.Client, testRegistry().Servers[0]) {
		t.Fatalf("bad server: %s", p.Content)
	}
	if p.Client == Cline || p.Client == Kilo {
		if p.Mode != "export" || p.RelativePath != "" {
			t.Fatal("editor export guessed a profile path")
		}
	}
}

func TestJSONMergePreservesOtherSettingsAndAccessPolicy(t *testing.T) {
	existing := []byte(`{"preferences":{"number":9007199254740993,"theme":"dark"},"mcpServers":{"other":{"command":"/other","env":{"TOKEN":"sensitive-existing-value"}},"praetor-dev":{"command":"/opt/praetor/bin/praetor-mcp","args":["-transport=stdio"],"trust":false,"disabled":true}}}`)
	registry := testRegistry()
	registry.Servers = append(registry.Servers, Server{Name: "second", Command: "/opt/second", Args: []string{}})
	p, err := BuildPlan(t.Context(), registry, Gemini, existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"9007199254740993", "sensitive-existing-value", `"trust": false`, `"disabled": true`} {
		if !bytes.Contains(p.Content, []byte(value)) {
			t.Fatalf("setting lost: %s", value)
		}
	}
	metadata, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, []byte("sensitive-existing-value")) {
		t.Fatal("candidate secret leaked through metadata")
	}
	if p.SourceSHA256 != digest(existing) {
		t.Fatal("source hash not bound to existing bytes")
	}
}

func TestBuildPlanConflictsAndMalformedConfigs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client Client
		raw    string
		want   error
	}{
		{"command conflict", Claude, `{"mcpServers":{"praetor-dev":{"command":"/other"}}}`, ErrConflict},
		{"args conflict", Gemini, `{"mcpServers":{"praetor-dev":{"command":"/opt/praetor/bin/praetor-mcp","args":[]}}}`, ErrConflict},
		{"remote conflict", Cline, `{"mcpServers":{"praetor-dev":{"command":"/opt/praetor/bin/praetor-mcp","args":["-transport=stdio"],"url":"https://other"}}}`, ErrConflict},
		{"null section", Claude, `{"mcpServers":null}`, nil},
		{"duplicate nested", Claude, `{"unknown":{"secret":"a","secret":"b"}}`, nil},
		{"duplicate root", Gemini, `{"mcpServers":{},"mcpServers":{}}`, nil},
		{"trailing document", OpenCodeV1, `{} {}`, nil},
		{"JSONC", Kilo, "{ // comment\n}", nil},
		{"null", Claude, `null`, nil},
		{"array", Gemini, `[]`, nil},
		{"malformed", Cline, `{"mcpServers":`, nil},
		{"invalid UTF8", Claude, string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}), nil},
		{"codex native", Codex, `model = "existing"`, ErrUnsupportedMerge},
		{"agy native", AGY, `{}`, ErrUnsupportedMerge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			before := bytes.Clone(raw)
			p, err := BuildPlan(t.Context(), testRegistry(), tc.client, raw)
			if err == nil || p != nil {
				t.Fatalf("expected failure, got %+v / %v", p, err)
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

func TestYAMLMergePreservesCommentsAndOtherServers(t *testing.T) {
	existing := []byte("# personal configuration\nname: Custom\nversion: 2.0.0\nschema: v1\nunknown: preserved\nmcpServers:\n  - name: other\n    command: /other\n    env:\n      TOKEN: private\n")
	p, err := BuildPlan(t.Context(), testRegistry(), Continue, existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"# personal configuration", "name: Custom", "version: 2.0.0", "unknown: preserved", "TOKEN: private", "name: other", "name: praetor-dev"} {
		if !bytes.Contains(p.Content, []byte(text)) {
			t.Fatalf("lost %q in %s", text, p.Content)
		}
	}
}

func TestYAMLRejectsAmbiguityAndConflicts(t *testing.T) {
	for _, raw := range []string{
		"name: a\nname: b\n", "name: a\n---\nname: b\n", "name: &name a\nother: *name\n", "schema: v2\n", "mcpServers: {}\n", "mcpServers: [null]\n",
		"mcpServers:\n  - name: same\n    command: /a\n  - name: same\n    command: /b\n", "mcpServers:\n  - name: praetor-dev\n    command: /wrong\n", "null\n", "[]\n", "? [a,b]\n: unsupported-key\n",
	} {
		p, err := BuildPlan(t.Context(), testRegistry(), Continue, []byte(raw))
		if err == nil || p != nil {
			t.Fatalf("accepted invalid YAML %q: %+v", raw, p)
		}
	}
}

func TestRegistryValidationAndBounds(t *testing.T) {
	valid := `{"version":1,"servers":[{"name":"praetor","command":"/opt/server","args":[]}]}`
	registry, err := DecodeRegistry(t.Context(), []byte(valid))
	if err != nil || registry.Version != 1 {
		t.Fatalf("decode registry: %v", err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"version":1`, `"version":1,"env":{}`, 1),
		strings.Replace(valid, `"args":[]`, `"args":[],"env":{"TOKEN":"private-secret"}`, 1), strings.Replace(valid, `"name":"praetor"`, `"name":"praetor","name":"other"`, 1),
		strings.Replace(valid, `"command":"/opt/server"`, `"command":null`, 1), strings.Replace(valid, `"command":"/opt/server"`, `"command":"relative"`, 1),
		strings.Replace(valid, `"args":[]`, `"args":["${HOME}"]`, 1), strings.Replace(valid, `"args":[]`, `"args":["{env:TOKEN}"]`, 1), "null", "{}", "{\"version\":1,\"servers\":[]}",
	} {
		if _, err := DecodeRegistry(t.Context(), []byte(raw)); err == nil {
			t.Fatalf("accepted registry %s", raw)
		} else if strings.Contains(err.Error(), "private-secret") {
			t.Fatal("registry value leaked in error")
		}
	}
	for _, edit := range []func(*Registry){
		func(r *Registry) { r.Servers[0].Name = "-invalid" }, func(r *Registry) { r.Servers[0].Name = strings.Repeat("a", 65) },
		func(r *Registry) { r.Servers[0].Command = "/opt/../server" }, func(r *Registry) { r.Servers[0].Args = []string{"bad\narg"} },
		func(r *Registry) { r.Servers[0].Args = make([]string, MaxArgs+1) }, func(r *Registry) { r.Servers[0].Args = []string{strings.Repeat("x", MaxValueBytes+1)} },
		func(r *Registry) { r.Servers = append(r.Servers, r.Servers[0]) },
	} {
		r := testRegistry()
		edit(&r)
		if _, err := BuildPlan(t.Context(), r, Claude, nil); err == nil {
			t.Fatal("accepted invalid typed registry")
		}
	}
}

func TestBuildPlanBoundaryCancellationOrderingAndSize(t *testing.T) {
	var nilContext context.Context
	if _, err := BuildPlan(nilContext, testRegistry(), Claude, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := DecodeRegistry(nilContext, []byte("{}")); err == nil {
		t.Fatal("nil decode context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := BuildPlan(ctx, testRegistry(), Claude, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if _, err := BuildPlan(t.Context(), testRegistry(), Client("unknown"), nil); err == nil {
		t.Fatal("unknown client accepted")
	}
	if _, err := BuildPlan(t.Context(), testRegistry(), Claude, make([]byte, MaxConfigBytes+1)); err == nil {
		t.Fatal("oversized config accepted")
	}
	if _, err := DecodeRegistry(t.Context(), make([]byte, MaxConfigBytes+1)); err == nil {
		t.Fatal("oversized registry accepted")
	}
	raw := []byte(`{"nested":` + strings.Repeat("[", 32) + `0` + strings.Repeat("]", 32) + `}`)
	if _, err := BuildPlan(t.Context(), testRegistry(), Claude, raw); err == nil {
		t.Fatal("excessive nesting accepted")
	}
	r := Registry{Version: 1}
	for i := MaxServers - 1; i >= 0; i-- {
		r.Servers = append(r.Servers, Server{Name: fmt.Sprintf("server-%02d", i), Command: "/opt/server", Args: []string{}})
	}
	before := r.Servers[0].Name
	p, err := BuildPlan(t.Context(), r, Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Servers[0].Name != before {
		t.Fatal("registry sorted in place")
	}
	if bytes.Index(p.Content, []byte("server-00")) > bytes.Index(p.Content, []byte("server-31")) {
		t.Fatal("output not deterministic")
	}
	r.Servers = append(r.Servers, Server{Name: "extra", Command: "/opt/server"})
	if _, err := BuildPlan(t.Context(), r, Claude, nil); err == nil {
		t.Fatal("too many servers accepted")
	}
}
