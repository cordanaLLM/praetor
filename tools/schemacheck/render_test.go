package schemacheck

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/clientschema"
	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

// modelsDev is the one external reference of the vendored opencode schema. Praetor renders no
// model catalog entry, so the referenced Model definition is replaced by an accept-all schema
// here, and docs/guides/client-schemas.md names the replacement.
var opencodeExternal = External{"https://models.dev/model-schema.json": `{"$defs":{"Model":{}}}`}

func externalFor(relPath string) External {
	if strings.HasPrefix(relPath, "opencode/") {
		return opencodeExternal
	}
	return nil
}

func compileVendored(t *testing.T, relPath string) *Schema {
	t.Helper()
	schema, err := CompileVendored(relPath, externalFor(relPath))
	if err != nil {
		t.Fatalf("compile %s: %v", relPath, err)
	}
	return schema
}

// renderedHooks renders every registration row of client into a fresh hook file, the bytes
// adoption writes (agenthook.NativeHooks merged by clientjson.PlanHooks).
func renderedHooks(t *testing.T, client string, existing []byte) []byte {
	t.Helper()
	file, ok := agenthook.NativeHookFile(client)
	if !ok {
		t.Fatalf("%s has no native hook file", client)
	}
	var hooks []clientjson.Hook
	seen := map[agenthook.Event]bool{}
	for _, row := range agenthook.Registrations(client) {
		if !seen[row.Event] {
			seen[row.Event] = true
			hooks = append(hooks, agenthook.NativeHooks(client, file, row.Event)...)
		}
	}
	plan, err := clientjson.PlanHooks(context.Background(), existing, hooks)
	if err != nil {
		t.Fatalf("PlanHooks(%s): %v", client, err)
	}
	return plan.Content
}

func renderedMCP(t *testing.T, client clientsetup.Client, existing []byte) []byte {
	t.Helper()
	registry := clientsetup.Registry{Version: 1, Servers: []clientsetup.Server{
		{Name: "praetor", Command: "/usr/local/bin/praetor-mcp", Args: []string{"--root", "/work/repo"}},
		{Name: "second", Command: "/usr/local/bin/other", Args: []string{}},
	}}
	plan, err := clientsetup.BuildPlan(context.Background(), registry, client, existing)
	if err != nil {
		t.Fatalf("BuildPlan(%s): %v", client, err)
	}
	return plan.Content
}

func readRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// rendering is one config praetor writes for one client and the pinned schema that governs it.
type rendering struct {
	name   string
	schema string
	doc    []byte
}

// renderings lists every client config praetor renders that an upstream schema covers. The
// formats without one are in docs/guides/client-schemas.md.
func renderings(t *testing.T) []rendering {
	t.Helper()
	unrelated := []byte(`{"ui": {"theme": "dark"}, "mcpServers": {"existing": {"command": "/bin/true", "args": []}}}`)
	geminiMerged := renderedMCP(t, clientsetup.Gemini, renderedHooks(t, "gemini", nil))
	return []rendering{
		{".claude/settings.json (adoption hooks)", "claude/claude-code-settings.json", renderedHooks(t, "claude", nil)},
		{".codex/hooks.json (adoption hooks)", "codex/config.schema.json", renderedHooks(t, "codex", nil)},
		{".gemini/settings.json (adoption hooks)", "gemini/settings.schema.json", renderedHooks(t, "gemini", nil)},
		{".gemini/settings.json (MCP registry)", "gemini/settings.schema.json", renderedMCP(t, clientsetup.Gemini, nil)},
		{".gemini/settings.json (MCP merged into hooks)", "gemini/settings.schema.json", geminiMerged},
		{".gemini/settings.json (MCP merged into other settings)", "gemini/settings.schema.json", renderedMCP(t, clientsetup.Gemini, unrelated)},
		{"opencode.json (MCP registry)", "opencode/config.json", renderedMCP(t, clientsetup.OpenCodeV1, nil)},
		{"opencode.json (MCP merged)", "opencode/config.json", renderedMCP(t, clientsetup.OpenCodeV1, []byte(`{"logLevel": "INFO", "username": "praetor"}`))},
		{"tracked .claude/settings.json", "claude/claude-code-settings.json", readRepoFile(t, ".claude/settings.json")},
		{"tracked .codex/hooks.json", "codex/config.schema.json", readRepoFile(t, ".codex/hooks.json")},
		{"tracked .gemini/settings.json", "gemini/settings.schema.json", readRepoFile(t, ".gemini/settings.json")},
	}
}

func TestRenderedClientConfigsValidateAgainstThePinnedSchemas(t *testing.T) {
	for _, r := range renderings(t) {
		t.Run(r.name, func(t *testing.T) {
			if err := compileVendored(t, r.schema).Validate(r.doc); err != nil {
				t.Fatalf("%s violates %s:\n%v\n%s", r.name, r.schema, err, r.doc)
			}
		})
	}
}

// TestEveryVendoredSchemaCompiles keeps a bump that breaks the oracle's reading of a schema from
// passing unnoticed.
func TestEveryVendoredSchemaCompiles(t *testing.T) {
	for _, relPath := range vendoredPaths(t) {
		t.Run(relPath, func(t *testing.T) { compileVendored(t, relPath) })
	}
}

// negatives are one document per schema family the oracle must refuse: a mutated rendering.
func TestEachSchemaRefusesAMutatedDocument(t *testing.T) {
	cases := []struct{ schema, doc, field string }{
		{"claude/claude-code-settings.json", `{"hooks": {"PreToolUse": "not a list"}}`, "/hooks/PreToolUse"},
		{"claude/claude-code-settings.json", `{"hooks": {"PreToolUsed": []}}`, "PreToolUsed"},
		{"gemini/settings.schema.json", `{"mcpServerz": {}}`, "mcpServerz"},
		{"gemini/settings.schema.json", `{"mcpServers": {"x": {"command": 3}}}`, "/mcpServers/x/command"},
		{"codex/config.schema.json", `{"hooks": 3}`, "/hooks"},
		{"opencode/config.json", `{"mcp": {"x": {"type": "local"}}}`, "command"},
		{"opencode/config.json", `{"mcp": {"x": {"type": "local", "command": "not-an-array"}}}`, "/mcp/x"},
	}
	for _, c := range cases {
		err := compileVendored(t, c.schema).Validate([]byte(c.doc))
		var violations *Violations
		if !errors.As(err, &violations) || !violations.Names(c.field) {
			t.Errorf("%s accepted or misreported %s: %v (want a violation naming %s)", c.schema, c.doc, err, c.field)
		}
	}
}

func vendoredPaths(t *testing.T) []string {
	t.Helper()
	paths, err := clientschema.VendoredPaths(vendorDir)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// mutate decodes the vendored schema, lets edit change it and compiles the result.
func mutate(t *testing.T, relPath string, edit func(root map[string]any)) *Schema {
	t.Helper()
	manifest := loadManifest(t)
	raw, err := manifest.Schema(vendorDir, relPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	edit(root)
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := Compile(relPath+"~mutated", encoded, externalFor(relPath))
	if err != nil {
		t.Fatalf("compile mutated %s: %v", relPath, err)
	}
	return schema
}

// renameMember renames the member from to to in the object at path below root.
func renameMember(t *testing.T, root map[string]any, path []string, from, to string) {
	t.Helper()
	node := root
	for _, step := range path {
		next, ok := node[step].(map[string]any)
		if !ok {
			t.Fatalf("schema has no object at %v", path)
		}
		node = next
	}
	value, ok := node[from]
	if !ok {
		t.Fatalf("schema object at %v has no member %q", path, from)
	}
	delete(node, from)
	node[to] = value
}

// TestPlantedSchemaRenameFailsAndNamesTheField is the failing-first case of rule 13 (#909): an
// upstream bump that renames a field praetor renders must fail a test that names the field. Each
// case passes against the pinned schema, fails against the planted rename, and passes again
// against the schema once it is restored.
func TestPlantedSchemaRenameFailsAndNamesTheField(t *testing.T) {
	cases := []struct {
		name, schema, doc, field string
		path                     []string
		from, to                 string
	}{
		{"claude hook event", "claude/claude-code-settings.json", ".claude/settings.json (adoption hooks)", "PreToolUse", []string{"properties", "hooks", "properties"}, "PreToolUse", "BeforeToolUse"},
		{"gemini mcp servers", "gemini/settings.schema.json", ".gemini/settings.json (MCP registry)", "mcpServers", []string{"properties"}, "mcpServers", "mcpServerList"},
		{"opencode mcp", "opencode/config.json", "opencode.json (MCP registry)", "mcp", []string{"$defs", "Config", "properties"}, "mcp", "mcpServers"},
		{"codex hooks", "codex/config.schema.json", ".codex/hooks.json (adoption hooks)", "hooks", []string{"properties"}, "hooks", "lifecycle_hooks"},
	}
	byName := map[string]rendering{}
	for _, r := range renderings(t) {
		byName[r.name] = r
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := byName[c.doc].doc
			if doc == nil {
				t.Fatalf("no rendering %q", c.doc)
			}
			if err := compileVendored(t, c.schema).Validate(doc); err != nil {
				t.Fatalf("pinned schema refuses the rendering before the plant: %v", err)
			}
			planted := mutate(t, c.schema, func(root map[string]any) { renameMember(t, root, c.path, c.from, c.to) })
			err := planted.Validate(doc)
			var violations *Violations
			if !errors.As(err, &violations) || !violations.Names(c.from) {
				t.Fatalf("planted rename of %q: Validate = %v, want a violation naming the field", c.from, err)
			}
			t.Logf("planted rename %s -> %s fails as expected: %s", c.from, c.to, strings.SplitN(violations.Error(), "\n", 3)[1])
			restored := mutate(t, c.schema, func(root map[string]any) {})
			if err := restored.Validate(doc); err != nil {
				t.Fatalf("restored schema refuses the rendering: %v", err)
			}
		})
	}
}
