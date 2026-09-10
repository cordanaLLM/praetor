package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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

	handler := func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		ruleID, _ := args["rule_id"].(string)
		return TextResult("Rule: " + ruleID), nil
	}

	tool, err := NewReadOnlyTool("standards_explain_rule", "Explain a specific HISS standard", schema, handler)
	if err != nil {
		t.Fatalf("unexpected error constructing read-only tool: %v", err)
	}

	// Verify annotations
	if !tool.Annotations.ReadOnlyHint {
		t.Errorf("expected readOnlyHint to be true")
	}
	if tool.Annotations.DestructiveHint {
		t.Errorf("expected destructiveHint to be false")
	}
	if !tool.Annotations.IdempotentHint {
		t.Errorf("expected idempotentHint to be true")
	}
	if tool.Annotations.OpenWorldHint {
		t.Errorf("expected openWorldHint to be false")
	}

	// Verify execution
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
	handler := func(ctx context.Context, args map[string]any) (*ToolResult, error) {
		return TextResult("Transpilation complete"), nil
	}

	tool, err := NewMutatingTool("standards_compile_context", "Compile canonical agent context", schema, handler, false, true)
	if err != nil {
		t.Fatalf("unexpected error constructing mutating tool: %v", err)
	}

	if tool.Annotations.ReadOnlyHint {
		t.Errorf("expected readOnlyHint to be false")
	}
	if tool.Annotations.DestructiveHint {
		t.Errorf("expected destructiveHint to be false")
	}
	if !tool.Annotations.IdempotentHint {
		t.Errorf("expected idempotentHint to be true")
	}
}

func TestBridge_Positive_SchemaTranslations(t *testing.T) {
	schema := ToolInputSchema{
		Type: "object",
		Properties: map[string]PropertySchema{
			"path": {
				Type:        "string",
				Description: "Target file path",
			},
		},
		Required: []string{"path"},
	}

	tool, err := NewReadOnlyTool("standards_audit", "Audit repository", schema, nil)
	if err != nil {
		t.Fatalf("tool creation failed: %v", err)
	}

	// 1. OpenAI Translation
	openAITool, err := ToOpenAITool(tool)
	if err != nil {
		t.Fatalf("OpenAI translation failed: %v", err)
	}
	if openAITool.Type != "function" || openAITool.Function.Name != "standards_audit" {
		t.Errorf("unexpected OpenAI tool format: %+v", openAITool)
	}
	openAIBytes, err := json.Marshal(openAITool)
	if err != nil {
		t.Fatalf("failed to marshal OpenAI tool: %v", err)
	}
	openAIStr := string(openAIBytes)
	if !strings.Contains(openAIStr, `"type":"function"`) || !strings.Contains(openAIStr, `"parameters"`) {
		t.Errorf("OpenAI JSON does not contain expected markers: %s", openAIStr)
	}

	// 2. Anthropic Translation
	anthropicTool, err := ToAnthropicTool(tool)
	if err != nil {
		t.Fatalf("Anthropic translation failed: %v", err)
	}
	if anthropicTool.Name != "standards_audit" || anthropicTool.InputSchema.Type != "object" {
		t.Errorf("unexpected Anthropic tool format: %+v", anthropicTool)
	}
	anthropicBytes, err := json.Marshal(anthropicTool)
	if err != nil {
		t.Fatalf("failed to marshal Anthropic tool: %v", err)
	}
	anthropicStr := string(anthropicBytes)
	if !strings.Contains(anthropicStr, `"input_schema"`) || !strings.Contains(anthropicStr, `"name":"standards_audit"`) {
		t.Errorf("Anthropic JSON does not contain expected markers: %s", anthropicStr)
	}

	// 3. Gemini Translation
	geminiFunc, err := ToGeminiFunction(tool)
	if err != nil {
		t.Fatalf("Gemini translation failed: %v", err)
	}
	if geminiFunc.Name != "standards_audit" || geminiFunc.Parameters.Type != "object" {
		t.Errorf("unexpected Gemini tool format: %+v", geminiFunc)
	}
	geminiBytes, err := json.Marshal(geminiFunc)
	if err != nil {
		t.Fatalf("failed to marshal Gemini function: %v", err)
	}
	geminiStr := string(geminiBytes)
	if !strings.Contains(geminiStr, `"parameters"`) || !strings.Contains(geminiStr, `"name":"standards_audit"`) {
		t.Errorf("Gemini JSON does not contain expected markers: %s", geminiStr)
	}
}

func TestBridge_Positive_BatchConversions(t *testing.T) {
	t1, err := NewReadOnlyTool("tool1", "desc1", ToolInputSchema{Type: "object"}, nil)
	if err != nil {
		t.Fatalf("tool1 err: %v", err)
	}
	t2, err := NewReadOnlyTool("tool2", "desc2", ToolInputSchema{Type: "object"}, nil)
	if err != nil {
		t.Fatalf("tool2 err: %v", err)
	}

	tools := []Tool{t1, t2}

	openaiList, err := ToOpenAITools(tools)
	if err != nil || len(openaiList) != 2 {
		t.Fatalf("expected 2 openai tools, got %d, err: %v", len(openaiList), err)
	}

	anthropicList, err := ToAnthropicTools(tools)
	if err != nil || len(anthropicList) != 2 {
		t.Fatalf("expected 2 anthropic tools, got %d, err: %v", len(anthropicList), err)
	}

	geminiList, err := ToGeminiFunctions(tools)
	if err != nil || len(geminiList) != 2 {
		t.Fatalf("expected 2 gemini functions, got %d, err: %v", len(geminiList), err)
	}
}

// Negative Tests
func TestTool_Negative_EmptyNameOrType(t *testing.T) {
	_, err := NewReadOnlyTool("", "description", ToolInputSchema{Type: "object"}, nil)
	if err == nil {
		t.Errorf("expected error constructing tool with empty name")
	}

	invalidTool := Tool{
		Name:        "invalid_tool",
		InputSchema: ToolInputSchema{Type: ""},
	}
	if err := invalidTool.Validate(); err == nil {
		t.Errorf("expected error validating tool with empty schema type")
	}

	if _, err := ToOpenAITool(invalidTool); err == nil {
		t.Errorf("expected error converting invalid tool to OpenAI format")
	}
	if _, err := ToAnthropicTool(invalidTool); err == nil {
		t.Errorf("expected error converting invalid tool to Anthropic format")
	}
	if _, err := ToGeminiFunction(invalidTool); err == nil {
		t.Errorf("expected error converting invalid tool to Gemini format")
	}
}

func TestBridge_Negative_BatchLimitExceeded(t *testing.T) {
	tools := make([]Tool, 501)
	validTool, err := NewReadOnlyTool("tool", "desc", ToolInputSchema{Type: "object"}, nil)
	if err != nil {
		t.Fatalf("failed tool init: %v", err)
	}
	for i := 0; i < 501; i++ {
		tools[i] = validTool
	}

	if _, err := ToOpenAITools(tools); err == nil {
		t.Errorf("expected error for OpenAI batch exceeding 500 limit")
	}
	if _, err := ToAnthropicTools(tools); err == nil {
		t.Errorf("expected error for Anthropic batch exceeding 500 limit")
	}
	if _, err := ToGeminiFunctions(tools); err == nil {
		t.Errorf("expected error for Gemini batch exceeding 500 limit")
	}
}

// Boundary Tests
func TestTool_Boundary_EmptyBatch(t *testing.T) {
	emptyTools := []Tool{}

	openai, err := ToOpenAITools(emptyTools)
	if err != nil || len(openai) != 0 {
		t.Errorf("expected empty OpenAI slice without error, got %v, err: %v", openai, err)
	}

	anthropic, err := ToAnthropicTools(emptyTools)
	if err != nil || len(anthropic) != 0 {
		t.Errorf("expected empty Anthropic slice without error, got %v, err: %v", anthropic, err)
	}

	gemini, err := ToGeminiFunctions(emptyTools)
	if err != nil || len(gemini) != 0 {
		t.Errorf("expected empty Gemini slice without error, got %v, err: %v", gemini, err)
	}
}

func TestTool_Boundary_MaxPropertiesAndDescriptions(t *testing.T) {
	props := make(map[string]PropertySchema, 50)
	for i := 0; i < 50; i++ {
		key := string(rune('a' + (i % 26))) + string(rune('0' + (i / 26)))
		props[key] = PropertySchema{
			Type:        "string",
			Description: "Property " + key,
		}
	}

	longDesc := strings.Repeat("A", 1000)
	tool, err := NewReadOnlyTool("boundary_tool", longDesc, ToolInputSchema{
		Type:       "object",
		Properties: props,
	}, nil)
	if err != nil {
		t.Fatalf("boundary tool creation failed: %v", err)
	}

	converted, err := ToOpenAITool(tool)
	if err != nil {
		t.Fatalf("OpenAI conversion of boundary tool failed: %v", err)
	}
	if len(converted.Function.Parameters.Properties) != 50 {
		t.Errorf("expected 50 properties, got %d", len(converted.Function.Parameters.Properties))
	}
}
