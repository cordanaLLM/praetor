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

// ErrNilHandler is returned by the tool constructors when no handler is supplied. A tool
// registered without a handler would compile and list fine but panic on its first
// tools/call, so the constructors refuse it up front.
var ErrNilHandler = errors.New("mcp: tool handler must not be nil")

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

// Validate checks that the Tool definition conforms to HISS invariants: a non-empty
// name and a typed input schema with at most MaxToolProperties entries. A nil
// Properties map is normalised to an empty one for JSON object serialization.
//
// Validate checks shape only. It does not require a Handler (tools/call reports a
// missing one per call) and does not scan for prompt injection; executable tools are built
// through the constructors, which enforce both.
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

// newTool assembles and validates an executable tool with the given annotations.
func newTool(kind, name, description string, schema ToolInputSchema, handler ToolHandler, ann ToolAnnotations) (Tool, error) {
	if handler == nil {
		return Tool{}, fmt.Errorf("failed to construct %s tool %s: %w", kind, name, ErrNilHandler)
	}
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
		Annotations: ann,
		Handler:     handler,
	}
	if err := tool.Validate(); err != nil {
		return Tool{}, fmt.Errorf("failed to construct %s tool %s: %w", kind, name, err)
	}
	// A descriptor is model-facing text served by tools/list; refuse one that reads as an
	// instruction override rather than serve it.
	if HasToolInjection(tool) {
		return Tool{}, fmt.Errorf("failed to construct %s tool %s: %w", kind, name, ErrToolInjection)
	}
	return tool, nil
}

// NewReadOnlyTool constructs a validated read-only, idempotent, closed-world MCP tool
// specification. Use it only for tools that neither modify state nor reach outside the
// server's own repository (no network, no subprocesses, no writes).
func NewReadOnlyTool(name string, description string, schema ToolInputSchema, handler ToolHandler) (Tool, error) {
	return newTool("read-only", name, description, schema, handler, ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	})
}

// NewMutatingTool constructs an MCP tool specification that modifies state inside the
// server's closed world. destructive must be true whenever the tool can overwrite or
// replace existing files (for example through a force flag).
func NewMutatingTool(name string, description string, schema ToolInputSchema, handler ToolHandler, destructive bool, idempotent bool) (Tool, error) {
	return newTool("mutating", name, description, schema, handler, ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: destructive,
		IdempotentHint:  idempotent,
		OpenWorldHint:   false,
	})
}

// NewOpenWorldTool constructs an MCP tool specification whose execution leaves the
// server's closed world: network egress, cloning remote repositories, spawning package
// managers or scanning directories outside the repository. readOnly reports whether the
// tool leaves the repository untouched; open-world tools are never marked destructive
// because their side effects land outside the governed tree.
func NewOpenWorldTool(name string, description string, schema ToolInputSchema, handler ToolHandler, readOnly bool, idempotent bool) (Tool, error) {
	return newTool("open-world", name, description, schema, handler, ToolAnnotations{
		ReadOnlyHint:    readOnly,
		DestructiveHint: false,
		IdempotentHint:  idempotent,
		OpenWorldHint:   true,
	})
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
