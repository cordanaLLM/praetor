package mcp

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

const (
	maxToolsBatchLimit = 500
	// maxSanitizedProperties bounds the schema properties neutralized per tool (HISS-02).
	maxSanitizedProperties = 500
)

// OpenAIFunction represents the function object inside an OpenAI tool descriptor.
type OpenAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  ToolInputSchema `json:"parameters"`
}

// OpenAITool represents the OpenAI Function Calling format.
type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// AnthropicTool represents the Anthropic Tool Use format.
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"input_schema"`
}

// GeminiFunctionDeclaration represents the Gemini Function Declaration format.
type GeminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  ToolInputSchema `json:"parameters"`
}

// prepareSchema ensures properties is non-nil for JSON schema compatibility and
// neutralizes prompt-injection payloads in every free-text description it carries.
//
// A tool descriptor is model-facing text: a third-party MCP server can place instruction
// overrides in a description and have them read as if they came from the operator. The
// `agent:sandboxed` facet promises that defense, so the bridge - the single point where a
// descriptor is translated for a model - is where it is applied.
func prepareSchema(schema ToolInputSchema) ToolInputSchema {
	if schema.Type == "" {
		schema.Type = "object"
	}
	if schema.Properties == nil {
		schema.Properties = make(map[string]PropertySchema)
		return schema
	}

	sanitized := make(map[string]PropertySchema, len(schema.Properties))
	count := 0
	for name, prop := range schema.Properties {
		if count >= maxSanitizedProperties {
			break
		}
		count++
		prop.Description = lockdown.SanitizePrompt(prop.Description)
		sanitized[name] = prop
	}
	schema.Properties = sanitized
	return schema
}

// HasToolInjection reports whether a tool descriptor carries a known prompt-injection
// pattern in any of its model-facing text fields.
func HasToolInjection(t Tool) bool {
	if lockdown.HasInjection(t.Description) || lockdown.HasInjection(t.Name) {
		return true
	}
	count := 0
	for _, prop := range t.InputSchema.Properties {
		if count >= maxSanitizedProperties {
			break
		}
		count++
		if lockdown.HasInjection(prop.Description) {
			return true
		}
	}
	return false
}

// ToOpenAITool converts an MCP Tool to the OpenAI Function Calling specification.
func ToOpenAITool(t Tool) (*OpenAITool, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to OpenAI format: %w", err)
	}
	schema := prepareSchema(t.InputSchema)
	description := lockdown.SanitizePrompt(t.Description)
	return &OpenAITool{
		Type: "function",
		Function: OpenAIFunction{
			Name:        t.Name,
			Description: description,
			Parameters:  schema,
		},
	}, nil
}

// ToAnthropicTool converts an MCP Tool to the Anthropic Tool Use specification.
func ToAnthropicTool(t Tool) (*AnthropicTool, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to Anthropic format: %w", err)
	}
	schema := prepareSchema(t.InputSchema)
	description := lockdown.SanitizePrompt(t.Description)
	return &AnthropicTool{
		Name:        t.Name,
		Description: description,
		InputSchema: schema,
	}, nil
}

// ToGeminiFunction converts an MCP Tool to the Gemini Function Declaration specification.
func ToGeminiFunction(t Tool) (*GeminiFunctionDeclaration, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to Gemini format: %w", err)
	}
	schema := prepareSchema(t.InputSchema)
	description := lockdown.SanitizePrompt(t.Description)
	return &GeminiFunctionDeclaration{
		Name:        t.Name,
		Description: description,
		Parameters:  schema,
	}, nil
}

// ToOpenAITools converts a slice of MCP Tools to OpenAI format with bounded execution.
func ToOpenAITools(tools []Tool) ([]OpenAITool, error) {
	if len(tools) > maxToolsBatchLimit {
		return nil, errors.New("tool batch exceeds maximum scalar bound (500)")
	}
	result := make([]OpenAITool, 0, len(tools))
	limit := len(tools)
	for i := 0; i < limit; i++ {
		converted, err := ToOpenAITool(tools[i])
		if err != nil {
			return nil, fmt.Errorf("failed translating tool index %d: %w", i, err)
		}
		result = append(result, *converted)
	}
	return result, nil
}

// ToAnthropicTools converts a slice of MCP Tools to Anthropic format with bounded execution.
func ToAnthropicTools(tools []Tool) ([]AnthropicTool, error) {
	if len(tools) > maxToolsBatchLimit {
		return nil, errors.New("tool batch exceeds maximum scalar bound (500)")
	}
	result := make([]AnthropicTool, 0, len(tools))
	limit := len(tools)
	for i := 0; i < limit; i++ {
		converted, err := ToAnthropicTool(tools[i])
		if err != nil {
			return nil, fmt.Errorf("failed translating tool index %d: %w", i, err)
		}
		result = append(result, *converted)
	}
	return result, nil
}

// ToGeminiFunctions converts a slice of MCP Tools to Gemini format with bounded execution.
func ToGeminiFunctions(tools []Tool) ([]GeminiFunctionDeclaration, error) {
	if len(tools) > maxToolsBatchLimit {
		return nil, errors.New("tool batch exceeds maximum scalar bound (500)")
	}
	result := make([]GeminiFunctionDeclaration, 0, len(tools))
	limit := len(tools)
	for i := 0; i < limit; i++ {
		converted, err := ToGeminiFunction(tools[i])
		if err != nil {
			return nil, fmt.Errorf("failed translating tool index %d: %w", i, err)
		}
		result = append(result, *converted)
	}
	return result, nil
}
