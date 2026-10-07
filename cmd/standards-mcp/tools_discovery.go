package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/mcp"
)

// Tool-list modes selected by -tools.
const (
	// toolsModeFull lists every registered tool with its full schema (the default).
	toolsModeFull = "full"
	// toolsModeIndex lists only the discovery tools and the hot tools; the rest is found
	// through standards_tools_index and standards_tool_describe.
	toolsModeIndex = "index"
)

// outputReadToolName names the offload read-back tool, whose output is never offloaded.
const outputReadToolName = "standards_output_read"

// maxSummaryRunes bounds the one-line summary in the tool index.
const maxSummaryRunes = 120

// indexModeTools names the tools tools/list still carries in index mode. tools/call
// accepts every registered tool in both modes.
var indexModeTools = map[string]bool{
	"standards_audit":           true,
	"standards_compile_context": true,
	"standards_tools_index":     true,
	"standards_tool_describe":   true,
}

// ErrToolsMode reports a -tools value other than full or index.
var ErrToolsMode = errors.New("standards-mcp: tools mode must be full or index")

// normalizeToolsMode maps "" to full and rejects an unknown mode.
func normalizeToolsMode(mode string) (string, error) {
	switch mode {
	case "", toolsModeFull:
		return toolsModeFull, nil
	case toolsModeIndex:
		return toolsModeIndex, nil
	default:
		return "", fmt.Errorf("%w: got %q", ErrToolsMode, mode)
	}
}

// createToolsIndexTool registers standards_tools_index.
func (s *Server) createToolsIndexTool() (mcp.Tool, error) {
	return mcp.NewReadOnlyTool("standards_tools_index",
		"List every standards tool: one-line summary, annotations. Static, sorted by name.",
		mcp.ToolInputSchema{Type: "object"}, s.toolsIndex)
}

// createToolDescribeTool registers standards_tool_describe.
func (s *Server) createToolDescribeTool() (mcp.Tool, error) {
	return mcp.NewReadOnlyTool("standards_tool_describe",
		"Return full description, input schema of one standards tool.",
		mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]mcp.PropertySchema{
				"name": {Type: "string", Description: "Tool name as listed by standards_tools_index"},
			},
			Required: []string{"name"},
		}, s.toolDescribe)
}

// createOutputReadTool registers standards_output_read.
func (s *Server) createOutputReadTool() (mcp.Tool, error) {
	return mcp.NewReadOnlyTool("standards_output_read",
		"Read offloaded tool output by pointer-line sha256. Max 12 KiB per call; continue from next_offset.",
		mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]mcp.PropertySchema{
				"sha256": {Type: "string", Description: "64 lowercase hex digits from [offloaded] pointer line"},
				"offset": {Type: "integer", Description: "Byte offset to start at (default 0)"},
				"limit":  {Type: "integer", Description: "Maximum bytes to return (default and maximum 12288)"},
			},
			Required: []string{"sha256"},
		}, s.outputRead)
}

// toolSummary is the one-line summary of a tool: the first sentence of its description,
// bounded to maxSummaryRunes.
func toolSummary(description string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(description), "\n")
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i+1]
	}
	runes := []rune(line)
	if len(runes) > maxSummaryRunes {
		return string(runes[:maxSummaryRunes-3]) + "..."
	}
	return line
}

// annotationFlags renders the true annotation hints of a tool, in a fixed order.
func annotationFlags(a mcp.ToolAnnotations) string {
	var flags []string
	if a.ReadOnlyHint {
		flags = append(flags, "readOnly")
	}
	if a.DestructiveHint {
		flags = append(flags, "destructive")
	}
	if a.IdempotentHint {
		flags = append(flags, "idempotent")
	}
	if a.OpenWorldHint {
		flags = append(flags, "openWorld")
	}
	if len(flags) == 0 {
		return "none"
	}
	return strings.Join(flags, ",")
}

// toolsIndex renders one line per registered tool, sorted by name. The text depends only on
// the registered descriptors, so it is byte-stable across calls.
func (s *Server) toolsIndex(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if len(args) != 0 {
		return mcpErrorResult("tools index accepts no arguments", mcpTextProtocol), nil
	}
	var b mcpTextBuilder
	for _, name := range s.order {
		tool := s.tools[name]
		b.Template("%s | %s | %s\n", tool.Name, toolSummary(tool.Description), annotationFlags(tool.Annotations))
	}
	return mcpComposedTextResult(b.Text()), nil
}

// toolDescribe returns the full descriptor of one tool as compact JSON.
func (s *Server) toolDescribe(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	name, err := argString(args, "name")
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	tool, ok := s.tools[name]
	if !ok {
		return mcpErrorResult("unknown tool; call standards_tools_index for the names", mcpTextProtocol), nil
	}
	data, err := json.Marshal(toolDescriptor(tool))
	if err != nil {
		return nil, fmt.Errorf("encode tool descriptor: %w", err)
	}
	return mcpTextResult(string(data), mcpTextStructuredJSON), nil
}

// toolDescriptor is the tools/list entry of one tool.
func toolDescriptor(tool mcp.Tool) map[string]any {
	return map[string]any{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
		"annotations": tool.Annotations,
	}
}

// outputRead serves standards_output_read.
func (s *Server) outputRead(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	digest, err := argString(args, "sha256")
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	offset, err := argInt(args, "offset")
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	limit, err := argInt(args, "limit")
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	chunk, next, total, err := s.offloader().Read(digest, offset, limit)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	header := fmt.Sprintf("[offloaded-read] sha256=%s offset=%d next_offset=%d bytes=%d\n", digest, offset, next, total)
	return mcpTextResult(header+chunk, mcpTextUntrusted), nil
}

// argInt reads an optional non-negative integer argument (0 when absent). JSON numbers
// decode to float64; a fractional or negative value is refused.
func argInt(args map[string]any, key string) (int, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, nil
	}
	f, isNumber := raw.(float64)
	if !isNumber {
		i, isInt := raw.(int)
		if !isInt {
			return 0, fmt.Errorf("%w: %s must be an integer, got %T", ErrArgType, key, raw)
		}
		f = float64(i)
	}
	if f < 0 || f != float64(int64(f)) || f > float64(mcp.MaxResultTextBytes) {
		return 0, fmt.Errorf("%w: %s must be an integer from 0 to %d", ErrArgType, key, mcp.MaxResultTextBytes)
	}
	return int(f), nil
}

// listedToolNames returns the tool names tools/list serves in the server's mode, sorted.
func (s *Server) listedToolNames() []string {
	if s.toolsMode == toolsModeFull {
		return s.order
	}
	names := make([]string, 0, len(indexModeTools))
	for _, name := range s.order {
		if indexModeTools[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
