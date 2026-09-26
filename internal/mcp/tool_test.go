package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// echoHandler is a minimal executable body for constructor tests.
func echoHandler(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ruleID, ok := args["rule_id"].(string)
	if !ok {
		return ErrorResult("rule_id must be a string"), nil
	}
	return TextResult("Rule: " + ruleID), nil
}

// Positive Tests

func TestTool_Positive_ReadOnlyAndAnnotations(t *testing.T) {
	schema := ToolInputSchema{
		Type: "object",
		Properties: map[string]PropertySchema{
			"rule_id": {
				Type:        "string",
				Description: "The HISS rule identifier to explain",
			},
		},
		Required: []string{"rule_id"},
	}

	tool, err := NewReadOnlyTool("standards_explain_rule", "Explain a specific HISS standard", schema, echoHandler)
	if err != nil {
		t.Fatalf("unexpected error constructing read-only tool: %v", err)
	}

	want := ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
	if tool.Annotations != want {
		t.Errorf("read-only annotations = %+v, want %+v", tool.Annotations, want)
	}

	res, err := tool.Handler(context.Background(), map[string]any{"rule_id": "HISS-01"})
	if err != nil {
		t.Fatalf("handler execution failed: %v", err)
	}
	if res.IsError || len(res.Content) != 1 || res.Content[0].Text != "Rule: HISS-01" {
		t.Errorf("unexpected tool execution result: %+v", res)
	}
}

func TestTool_Positive_MutatingTool(t *testing.T) {
	schema := ToolInputSchema{
		Type: "object",
		Properties: map[string]PropertySchema{
			"source": {Type: "string", Description: "Source AGENTS.md path"},
		},
	}

	additive, err := NewMutatingTool("standards_compile_context", "Compile canonical agent context", schema, echoHandler, false, true)
	if err != nil {
		t.Fatalf("unexpected error constructing mutating tool: %v", err)
	}
	want := ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
	if additive.Annotations != want {
		t.Errorf("mutating annotations = %+v, want %+v", additive.Annotations, want)
	}

	destructive, err := NewMutatingTool("standards_adopt", "Adopt with force", schema, echoHandler, true, false)
	if err != nil {
		t.Fatalf("unexpected error constructing destructive tool: %v", err)
	}
	if !destructive.Annotations.DestructiveHint || destructive.Annotations.IdempotentHint {
		t.Errorf("destructive annotations not propagated: %+v", destructive.Annotations)
	}
}

func TestTool_Positive_OpenWorldTool(t *testing.T) {
	readOnly, err := NewOpenWorldTool("standards_version_audit", "Query upstream registries", ToolInputSchema{}, echoHandler, true, true)
	if err != nil {
		t.Fatalf("unexpected error constructing open-world tool: %v", err)
	}
	want := ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	if readOnly.Annotations != want {
		t.Errorf("open-world read-only annotations = %+v, want %+v", readOnly.Annotations, want)
	}

	mutating, err := NewOpenWorldTool("standards_dogfood", "Clone remote repositories", ToolInputSchema{}, echoHandler, false, true)
	if err != nil {
		t.Fatalf("unexpected error constructing open-world mutating tool: %v", err)
	}
	if mutating.Annotations.ReadOnlyHint || !mutating.Annotations.OpenWorldHint {
		t.Errorf("open-world mutating annotations = %+v", mutating.Annotations)
	}
}

func TestTool_Positive_ResultConstructors(t *testing.T) {
	ok := TextResult("all good")
	if ok.IsError || len(ok.Content) != 1 || ok.Content[0].Type != "text" || ok.Content[0].Text != "all good" {
		t.Errorf("TextResult = %+v", ok)
	}

	bad := ErrorResult("boom")
	if !bad.IsError || len(bad.Content) != 1 || bad.Content[0].Type != "text" || bad.Content[0].Text != "boom" {
		t.Errorf("ErrorResult = %+v", bad)
	}

	data, err := json.Marshal(bad)
	if err != nil {
		t.Fatalf("marshal error result: %v", err)
	}
	if !strings.Contains(string(data), `"isError":true`) {
		t.Errorf("ErrorResult JSON lacks isError flag: %s", data)
	}
}

// Negative Tests

func TestTool_Negative_EmptyNameOrType(t *testing.T) {
	if _, err := NewReadOnlyTool("", "description", ToolInputSchema{Type: "object"}, echoHandler); err == nil {
		t.Errorf("expected error constructing read-only tool with empty name")
	}
	if _, err := NewMutatingTool("", "description", ToolInputSchema{Type: "object"}, echoHandler, true, true); err == nil {
		t.Errorf("expected error constructing mutating tool with empty name")
	}
	if _, err := NewOpenWorldTool("", "description", ToolInputSchema{Type: "object"}, echoHandler, true, true); err == nil {
		t.Errorf("expected error constructing open-world tool with empty name")
	}

	invalidTool := Tool{
		Name:        "invalid_tool",
		InputSchema: ToolInputSchema{Type: ""},
	}
	if err := invalidTool.Validate(); err == nil {
		t.Errorf("expected error validating tool with empty schema type")
	}

}

func TestTool_Negative_NilHandler(t *testing.T) {
	if _, err := NewReadOnlyTool("ro", "desc", ToolInputSchema{Type: "object"}, nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("read-only constructor with nil handler: got %v, want ErrNilHandler", err)
	}
	if _, err := NewMutatingTool("mut", "desc", ToolInputSchema{Type: "object"}, nil, false, true); !errors.Is(err, ErrNilHandler) {
		t.Errorf("mutating constructor with nil handler: got %v, want ErrNilHandler", err)
	}
	if _, err := NewOpenWorldTool("ow", "desc", ToolInputSchema{Type: "object"}, nil, true, true); !errors.Is(err, ErrNilHandler) {
		t.Errorf("open-world constructor with nil handler: got %v, want ErrNilHandler", err)
	}
}

// Boundary Tests

func TestTool_Boundary_SchemaDefaults(t *testing.T) {
	// Boundary: an empty schema is normalised to an object with an empty property map by
	// every constructor, so the wire form is always {"type":"object","properties":{}}.
	cases := []struct {
		name string
		make func() (Tool, error)
	}{
		{"read-only", func() (Tool, error) { return NewReadOnlyTool("ro", "d", ToolInputSchema{}, echoHandler) }},
		{"mutating", func() (Tool, error) { return NewMutatingTool("mut", "d", ToolInputSchema{}, echoHandler, false, true) }},
		{"open-world", func() (Tool, error) { return NewOpenWorldTool("ow", "d", ToolInputSchema{}, echoHandler, true, true) }},
	}
	for _, tc := range cases {
		tool, err := tc.make()
		if err != nil {
			t.Fatalf("%s: constructor with empty schema failed: %v", tc.name, err)
		}
		if tool.InputSchema.Type != "object" || tool.InputSchema.Properties == nil {
			t.Errorf("%s: schema not normalised: %+v", tc.name, tool.InputSchema)
		}
		data, err := json.Marshal(tool.InputSchema)
		if err != nil || !strings.Contains(string(data), `"properties":{}`) {
			t.Errorf("%s: schema JSON = %s (err %v), want empty properties object", tc.name, data, err)
		}
	}

	// Boundary: Validate on a typed schema with nil properties repairs the map in place.
	spec := Tool{Name: "spec", InputSchema: ToolInputSchema{Type: "object"}}
	if err := spec.Validate(); err != nil || spec.InputSchema.Properties == nil {
		t.Errorf("Validate did not normalise nil properties: err=%v schema=%+v", err, spec.InputSchema)
	}
}

func TestTool_Boundary_MaxPropertiesAndDescriptions(t *testing.T) {
	props := make(map[string]PropertySchema, 50)
	for i := 0; i < 50; i++ {
		key := string(rune('a'+(i%26))) + string(rune('0'+(i/26)))
		props[key] = PropertySchema{
			Type:        "string",
			Description: "Property " + key,
		}
	}

	longDesc := strings.Repeat("A", 1000)
	tool, err := NewReadOnlyTool("boundary_tool", longDesc, ToolInputSchema{
		Type:       "object",
		Properties: props,
	}, echoHandler)
	if err != nil {
		t.Fatalf("boundary tool creation failed: %v", err)
	}

	if len(tool.InputSchema.Properties) != 50 || tool.Description != longDesc {
		t.Errorf("constructor altered the boundary tool: %d properties", len(tool.InputSchema.Properties))
	}
}
