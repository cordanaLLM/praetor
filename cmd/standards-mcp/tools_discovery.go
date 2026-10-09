package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
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
const outputReadToolName = mcp.OffloadReadToolName

// manifestReadTimeout bounds the one manifest read at server start (HISS-02).
const manifestReadTimeout = 10 * time.Second

// maxSummaryRunes bounds the one-line summary in the tool index.
const maxSummaryRunes = 120

// maxListedDescriptionBytes is the longest description (of a tool or of one property) that
// tools/list serves whole. A longer one is cut to its summary there, and
// standards_tool_describe returns it in full, so the listing stays small in both modes.
const maxListedDescriptionBytes = 200

// describeHint ends a description that tools/list cut short.
const describeHint = " Full text: standards_tool_describe."

// indexModeTools names the tools tools/list still carries in index mode. tools/call
// accepts every registered tool in both modes.
var indexModeTools = map[string]bool{
	"standards_audit":           true,
	"standards_compile_context": true,
	"standards_tools_index":     true,
	"standards_tool_describe":   true,
	outputReadToolName:          true,
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

// toolDescriptor is the full descriptor of one tool, as standards_tool_describe returns it.
func toolDescriptor(tool mcp.Tool) map[string]any {
	return map[string]any{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": tool.InputSchema,
		"annotations": tool.Annotations,
	}
}

// listedDescriptor is the tools/list entry of one tool: toolDescriptor with every description
// above maxListedDescriptionBytes cut to its summary. The prose moves to the describe text.
func listedDescriptor(tool mcp.Tool) map[string]any {
	listed := tool
	listed.Description = listedDescription(tool.Description)
	properties := make(map[string]mcp.PropertySchema, len(tool.InputSchema.Properties))
	for name, property := range tool.InputSchema.Properties {
		property.Description = listedDescription(property.Description)
		properties[name] = property
	}
	listed.InputSchema.Properties = properties
	return toolDescriptor(listed)
}

// listedDescription returns description unchanged when it fits maxListedDescriptionBytes, else
// its summary followed by describeHint.
func listedDescription(description string) string {
	if len(description) <= maxListedDescriptionBytes {
		return description
	}
	return toolSummary(description) + describeHint
}

// outputRead serves standards_output_read.
func (s *Server) outputRead(_ context.Context, args map[string]any) (*mcp.ToolResult, error) {
	digest, err := argString(args, "sha256")
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	offset, err := boundedIntArg(args, "offset", 0, mcp.MaxResultTextBytes, 0)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextProtocol), nil
	}
	limit, err := boundedIntArg(args, "limit", 0, mcp.MaxResultTextBytes, 0)
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

// intArg reads an optional JSON integer argument. present reports whether the key was
// supplied at all; whole is false for a value that is not a whole number within the exact
// integer range of a float64 (a string, null, a fraction, an infinity). JSON numbers decode
// to float64; in-process callers pass int.
func intArg(args map[string]any, key string) (value int, present, whole bool) {
	raw, present := args[key]
	if !present {
		return 0, false, false
	}
	switch number := raw.(type) {
	case int:
		return number, true, true
	case float64:
		if number != math.Trunc(number) || math.Abs(number) > 1<<53 {
			return 0, true, false
		}
		return int(number), true, true
	}
	return 0, true, false
}

// boundedIntArg reads an optional integer argument within [minimum, maximum], def when the key
// is absent. It is the one bounded-integer reader of the tool handlers.
func boundedIntArg(args map[string]any, key string, minimum, maximum, def int) (int, error) {
	value, present, whole := intArg(args, key)
	if !present {
		return def, nil
	}
	if !whole || value < minimum || value > maximum {
		return 0, fmt.Errorf("%w: %s must be an integer from %d to %d", ErrArgType, key, minimum, maximum)
	}
	return value, nil
}

// resolveOffloadThreshold returns the offload threshold of a server rooted at root: requested
// when the caller set one, else the mcp.offload_threshold_bytes key of the manifest (0 there
// opts out of offloading), else 0 for the default. Only the mcp section decides the start: an
// invalid mcp section fails it, because a value the operator wrote must not silently select
// the default. A manifest that is broken elsewhere (unknown key, YAML typo) starts the server
// with the default threshold and a non-empty notice naming the substitution, so the tools that
// repair the manifest (standards_audit, standards_adopt) stay available.
func resolveOffloadThreshold(root string, requested int) (threshold int, notice string, err error) {
	if requested != 0 {
		return requested, "", nil
	}
	path := filepath.Join(root, config.ManifestFileName)
	ctx, cancel := context.WithTimeout(context.Background(), manifestReadTimeout)
	defer cancel()
	var doc yaml.Node
	err = config.ReadYAMLDocument(ctx, path, &doc, util.YAMLDocumentOptions{AllowEmpty: true})
	if errors.Is(err, os.ErrNotExist) {
		return 0, "", nil
	}
	if err != nil {
		return 0, fmt.Sprintf("standards-mcp: manifest %s unreadable (%v); offload threshold default %d bytes",
			config.ManifestFileName, err, mcp.OffloadThresholdBytes), nil
	}
	var section struct {
		MCP *config.MCPPolicy `yaml:"mcp"`
	}
	if err := doc.Decode(&section); err != nil {
		return 0, "", fmt.Errorf("read offload threshold from %s: %w", config.ManifestFileName, err)
	}
	if _, err := config.LoadManifest(path); err != nil {
		notice = fmt.Sprintf("standards-mcp: manifest %s invalid outside mcp section (%v); mcp section honored",
			config.ManifestFileName, err)
	}
	switch {
	case section.MCP == nil || section.MCP.OffloadThresholdBytes == nil:
		return 0, notice, nil
	case *section.MCP.OffloadThresholdBytes == 0:
		return mcp.OffloadDisabled, notice, nil
	}
	return *section.MCP.OffloadThresholdBytes, notice, nil
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
