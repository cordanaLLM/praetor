package schemacheck

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cordanaLLM/praetor/internal/mcpwire"
)

// roundTrip decodes doc into the generated type v points to and returns the re-encoded JSON.
func roundTrip(t *testing.T, doc string, v any) []byte {
	t.Helper()
	if err := json.Unmarshal([]byte(doc), v); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return out
}

func equalJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("re-encoded document is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("round trip changed the document:\n got: %s\nwant: %s", got, want)
	}
}

// TestGeneratedMCPTypesRoundTripWithoutLoss decodes valid MCP messages (each validated against
// the vendored schema first and after) into the generated types and encodes them again. A tool
// whose input schema says additionalProperties false, a result that carries a member the schema
// does not name, an empty capability object and an extension member all survive: the generated
// structs keep what they do not name instead of dropping it.
func TestGeneratedMCPTypesRoundTripWithoutLoss(t *testing.T) {
	cases := map[string]struct {
		doc    string
		target func() any
	}{
		"ListToolsResult": {`{"tools":[{"name":"audit","description":"d","x-vendor":{"k":[1,2]},"inputSchema":{"type":"object","properties":{"rule_id":{"type":"string","description":"id"}},"required":["rule_id"],"additionalProperties":false,"$defs":{"a":{"type":"integer"}}}}],"nextCursor":"c"}`,
			func() any { return new(mcpwire.ListToolsResult) }},
		"JSONRPCResultResponse": {`{"jsonrpc":"2.0","id":7,"result":{"tools":[{"name":"t","inputSchema":{"type":"object"}}],"_meta":{"a":1}}}`,
			func() any { return new(mcpwire.JSONRPCResultResponse) }},
		"InitializeResult": {`{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true},"experimental":{"x":{"y":1}},"logging":{}},"serverInfo":{"name":"s","version":"1"},"x-extension":true}`,
			func() any { return new(mcpwire.InitializeResult) }},
		"CallToolResult": {`{"content":[{"type":"text","text":"ok","annotations":{"audience":["user"],"vendor":1}}],"isError":false,"structuredContent":{"a":{"b":[]}}}`,
			func() any { return new(mcpwire.CallToolResult) }},
	}
	for def, c := range cases {
		t.Run(def, func(t *testing.T) {
			schema := mcpDefinition(t, def)
			if err := schema.Validate([]byte(c.doc)); err != nil {
				t.Fatalf("the sample is not a valid %s: %v", def, err)
			}
			out := roundTrip(t, c.doc, c.target())
			equalJSON(t, out, c.doc)
			if err := schema.Validate(out); err != nil {
				t.Fatalf("the re-encoded %s is invalid: %v", def, err)
			}
		})
	}
}

// TestRoundTripKeepsNamedMembersTyped is the other side: the named members are still typed
// fields, and the member the schema does not name sits in Extra.
func TestRoundTripKeepsNamedMembersTyped(t *testing.T) {
	var list mcpwire.ListToolsResult
	roundTrip(t, `{"tools":[],"nextCursor":"c","x-extension":1}`, &list)
	if list.NextCursor == nil || *list.NextCursor != "c" || string(list.Extra["x-extension"]) != "1" {
		t.Fatalf("decoded %+v", list)
	}
	var bad mcpwire.ListToolsResult
	if err := json.Unmarshal([]byte(`{"tools":"no"}`), &bad); err == nil {
		t.Fatal("a wrongly typed named member was accepted")
	}
}
