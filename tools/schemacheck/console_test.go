package schemacheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The console (console/) reads three data files, each a $defs entry of console/console.schema.json.
// Its fixtures are synthetic (console/fixtures/generate.py); these tests keep them valid and prove the
// schema refuses what it must.
const consoleSchemaPath = "console/console.schema.json"

var consoleFiles = []string{"cards", "graph", "status"}

func consoleDefinition(t *testing.T, def string) *Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(consoleSchemaPath)))
	if err != nil {
		t.Fatalf("read %s: %v", consoleSchemaPath, err)
	}
	schema, err := CompileDefinition(consoleSchemaPath, raw, def, nil)
	if err != nil {
		t.Fatalf("compile %s#%s: %v", consoleSchemaPath, def, err)
	}
	return schema
}

func consoleFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "console", "fixtures", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture %s is not JSON: %v", name, err)
	}
	return doc
}

// TestConsoleFixturesMatchSchema: every committed fixture validates against its definition.
func TestConsoleFixturesMatchSchema(t *testing.T) {
	for _, name := range consoleFiles {
		if err := consoleDefinition(t, name).ValidateValue(consoleFixture(t, name)); err != nil {
			t.Errorf("fixture %s: %v", name, err)
		}
	}
}

// TestConsoleSchemaRefusesPlantedFaults: an unknown field, a card state outside the enum, a wrong
// version and a malformed time are each refused and named.
func TestConsoleSchemaRefusesPlantedFaults(t *testing.T) {
	cases := []struct {
		def, field string
		plant      func(map[string]any)
	}{
		{"cards", "/cards/0/state", func(d map[string]any) { card(t, d)["state"] = "soon" }},
		{"cards", "/cards/0", func(d map[string]any) { card(t, d)["owner_email"] = "x" }},
		{"graph", "/version", func(d map[string]any) { d["version"] = 2 }},
		{"status", "/generated_at", func(d map[string]any) { d["generated_at"] = "yesterday" }},
	}
	for _, tc := range cases {
		doc := consoleFixture(t, tc.def)
		tc.plant(doc)
		err := consoleDefinition(t, tc.def).ValidateValue(doc)
		var violations *Violations
		if !errors.As(err, &violations) || !violations.Names(tc.field) {
			t.Errorf("%s: planted fault at %s not named: %v", tc.def, tc.field, err)
		}
	}
}

// TestConsoleSchemaBoundaries: empty lists are valid; a list one past its maxItems is refused.
func TestConsoleSchemaBoundaries(t *testing.T) {
	doc := consoleFixture(t, "cards")
	doc["cards"] = []any{}
	if err := consoleDefinition(t, "cards").ValidateValue(doc); err != nil {
		t.Errorf("an empty card list is valid: %v", err)
	}
	holds := make([]any, 101)
	for i := range holds {
		holds[i] = map[string]any{"id": "h"}
	}
	status := consoleFixture(t, "status")
	status["holds"] = holds
	if err := consoleDefinition(t, "status").ValidateValue(status); err == nil {
		t.Error("101 holds passed a maxItems of 100")
	}
}

func card(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	cards, ok := doc["cards"].([]any)
	if !ok || len(cards) == 0 {
		t.Fatalf("fixture cards is not a non-empty list: %v", doc["cards"])
	}
	first, ok := cards[0].(map[string]any)
	if !ok {
		t.Fatalf("fixture card 0 is not an object: %v", cards[0])
	}
	return first
}
