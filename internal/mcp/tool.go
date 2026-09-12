package mcp

import (
	"context"
	"errors"
	"fmt"
)

// MaxToolProperties bounds the number of schema properties a tool may declare.
const MaxToolProperties = 500

// ErrTooManyProperties reports a tool schema that exceeds MaxToolProperties.
var ErrTooManyProperties = errors.New("mcp: tool schema exceeds maximum property count")

// ToolAnnotations defines behavioral metadata hints for an MCP tool.
type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// PropertySchema defines a JSON Schema property descriptor for tool input parameters.
type PropertySchema struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

// ToolInputSchema defines the schema for tool arguments.
type ToolInputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]PropertySchema `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

// ContentItem represents a typed content payload in an MCP tool execution result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult represents the output of an executed MCP tool.
type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolHandler executes an MCP tool with provided arguments under an explicit context.
type ToolHandler func(ctx context.Context, args map[string]any) (*ToolResult, error)

// Tool defines an MCP tool specification including behavioral annotations.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema ToolInputSchema `json:"inputSchema"`
	Annotations ToolAnnotations `json:"annotations"`
	Handler     ToolHandler     `json:"-"`
}

// Validate checks that the Tool definition conforms to HISS-16 invariants.
func (t *Tool) Validate() error {
	if t.Name == "" {
		return errors.New("tool name cannot be empty")
	}
	if t.InputSchema.Type == "" {
		return fmt.Errorf("tool %q input schema type cannot be empty", t.Name)
	}
	if len(t.InputSchema.Properties) > MaxToolProperties {
		return fmt.Errorf("tool %q: %w: got %d, maximum %d", t.Name, ErrTooManyProperties, len(t.InputSchema.Properties), MaxToolProperties)
	}
	if t.InputSchema.Properties == nil {
		t.InputSchema.Properties = make(map[string]PropertySchema)
	}
	return nil
}

// NewReadOnlyTool constructs a validated read-only, idempotent MCP tool specification.
func NewReadOnlyTool(name string, description string, schema ToolInputSchema, handler ToolHandler) (Tool, error) {
	if schema.Properties == nil {
		schema.Properties = make(map[string]PropertySchema)
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	tool := Tool{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Annotations: ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: false,
			IdempotentHint:  true,
			OpenWorldHint:   false,
		},
		Handler: handler,
	}
	if err := tool.Validate(); err != nil {
		return Tool{}, fmt.Errorf("failed to construct read-only tool %s: %w", name, err)
	}
	return tool, nil
}

// NewMutatingTool constructs an MCP tool specification that modifies state.
func NewMutatingTool(name string, description string, schema ToolInputSchema, handler ToolHandler, destructive bool, idempotent bool) (Tool, error) {
	if schema.Properties == nil {
		schema.Properties = make(map[string]PropertySchema)
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	tool := Tool{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Annotations: ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: destructive,
			IdempotentHint:  idempotent,
			OpenWorldHint:   false,
		},
		Handler: handler,
	}
	if err := tool.Validate(); err != nil {
		return Tool{}, fmt.Errorf("failed to construct mutating tool %s: %w", name, err)
	}
	return tool, nil
}

// TextResult formats a successful plaintext MCP tool result.
func TextResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ContentItem{
			{
				Type: "text",
				Text: text,
			},
		},
		IsError: false,
	}
}

// ErrorResult formats an error MCP tool result.
func ErrorResult(errText string) *ToolResult {
	return &ToolResult{
		Content: []ContentItem{
			{
				Type: "text",
				Text: errText,
			},
		},
		IsError: true,
	}
}
