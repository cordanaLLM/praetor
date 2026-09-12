package mcp

import (
	"errors"
	"fmt"
)

// MaxToolsBatchLimit is the scalar bound on a single batch conversion (HISS-02).
const MaxToolsBatchLimit = 500

// ErrBatchTooLarge is returned when a batch conversion exceeds MaxToolsBatchLimit.
var ErrBatchTooLarge = errors.New("tool batch exceeds maximum scalar bound (500)")

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

// ToOpenAITool converts an MCP Tool to the OpenAI Function Calling specification.
// Validate rejects an untyped schema and normalises a nil Properties map, so the schema
// is used as-is afterwards.
func ToOpenAITool(t Tool) (*OpenAITool, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to OpenAI format: %w", err)
	}
	return &OpenAITool{
		Type: "function",
		Function: OpenAIFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		},
	}, nil
}

// ToAnthropicTool converts an MCP Tool to the Anthropic Tool Use specification.
func ToAnthropicTool(t Tool) (*AnthropicTool, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to Anthropic format: %w", err)
	}
	return &AnthropicTool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
	}, nil
}

// ToGeminiFunction converts an MCP Tool to the Gemini Function Declaration specification.
func ToGeminiFunction(t Tool) (*GeminiFunctionDeclaration, error) {
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("cannot convert invalid tool to Gemini format: %w", err)
	}
	return &GeminiFunctionDeclaration{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  t.InputSchema,
	}, nil
}

// ToOpenAITools converts a slice of MCP Tools to OpenAI format with bounded execution.
func ToOpenAITools(tools []Tool) ([]OpenAITool, error) {
	if len(tools) > MaxToolsBatchLimit {
		return nil, ErrBatchTooLarge
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
	if len(tools) > MaxToolsBatchLimit {
		return nil, ErrBatchTooLarge
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
	if len(tools) > MaxToolsBatchLimit {
		return nil, ErrBatchTooLarge
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
