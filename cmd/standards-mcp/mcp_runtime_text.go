package main

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/mcp"
)

type mcpTextClassification uint8
type mcpGovernedText string

type mcpTextBuilder struct {
	value strings.Builder
}

const (
	mcpTextStructuredJSON mcpTextClassification = iota + 1
	mcpTextUntrusted
	mcpTextProtocol
)

func mcpTextResult(text string, _ mcpTextClassification) *mcp.ToolResult {
	return mcp.TextResult(text)
}

func mcpErrorResult(text string, _ mcpTextClassification) *mcp.ToolResult {
	return mcp.ErrorResult(text)
}

func mcpClassifiedText(text string, _ mcpTextClassification) string {
	return text
}

func mcpTextf(template string, args ...any) mcpGovernedText {
	return mcpGovernedText(fmt.Sprintf(template, args...))
}

func (b *mcpTextBuilder) Template(template string, args ...any) {
	fmt.Fprintf(&b.value, template, args...)
}

func (b *mcpTextBuilder) External(text string, _ mcpTextClassification) {
	b.value.WriteString(text)
}

func (b *mcpTextBuilder) Append(text mcpGovernedText) {
	b.value.WriteString(string(text))
}

func (b *mcpTextBuilder) Text() mcpGovernedText {
	return mcpGovernedText(b.value.String())
}

func mcpComposedTextResult(text mcpGovernedText) *mcp.ToolResult {
	return mcp.TextResult(string(text))
}

func mcpComposedErrorResult(text mcpGovernedText) *mcp.ToolResult {
	return mcp.ErrorResult(string(text))
}
