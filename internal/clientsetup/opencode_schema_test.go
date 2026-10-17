package clientsetup

import (
	"bytes"
	"strings"
	"testing"
)

// opencode writes "$schema" into a loaded opencode.json that names none, so a file adoption
// wrote without it turned dirty on the first opencode run (ledger
// engine-opencode-template-schema-20261017).

func TestOpenCodePlanCarriesSchema_Positive(t *testing.T) {
	for name, existing := range map[string]string{
		"new file":            "",
		"file without schema": `{"theme":"dark","mcp":{"other":{"type":"local","command":["/other"]}}}`,
	} {
		p, err := BuildPlan(t.Context(), testRegistry(), OpenCodeV1, []byte(existing))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(p.Content), `"$schema": `+openCodeSchema) {
			t.Fatalf("%s: opencode.json without the schema opencode writes:\n%s", name, p.Content)
		}
		if strings.Index(string(p.Content), `"$schema"`) > strings.Index(string(p.Content), `"mcp"`) {
			t.Fatalf("%s: the schema is not the first key, where opencode puts it:\n%s", name, p.Content)
		}
		again, err := BuildPlan(t.Context(), testRegistry(), OpenCodeV1, p.Content)
		if err != nil || !bytes.Equal(again.Content, p.Content) {
			t.Fatalf("%s: a second merge changed the file: %v\n%s", name, err, again.Content)
		}
	}
}

func TestOpenCodePlanKeepsOwnSchema_Negative(t *testing.T) {
	existing := `{"$schema":"https://example.invalid/own.json","mcp":{}}`
	p, err := BuildPlan(t.Context(), testRegistry(), OpenCodeV1, []byte(existing))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Content), "https://example.invalid/own.json") || strings.Contains(string(p.Content), openCodeSchema) {
		t.Fatalf("a schema the file names was replaced:\n%s", p.Content)
	}
}

func TestOtherClientsGetNoOpenCodeSchema_Boundary(t *testing.T) {
	for _, client := range []Client{Claude, Gemini, Kilo} {
		p, err := BuildPlan(t.Context(), testRegistry(), client, nil)
		if err != nil {
			t.Fatalf("%s: %v", client, err)
		}
		if strings.Contains(string(p.Content), "$schema") {
			t.Fatalf("%s got the OpenCode schema:\n%s", client, p.Content)
		}
	}
}
