package mcp

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

const (
	// MaxResultContentItems bounds the content items one tool result may carry (HISS-02).
	MaxResultContentItems = 64
	// MaxResultTextBytes bounds the text one tool result, or one error message, may carry
	// through the prompt-injection neutralizer. 4 MiB is about a million tokens, more than a
	// client can place in any model context, and scans in under a second; larger output is
	// withheld rather than served unsanitized.
	MaxResultTextBytes = 4 << 20
)

var (
	// ErrToolInjection reports a tool descriptor whose name, description or property
	// descriptions match a prompt-injection pattern.
	ErrToolInjection = errors.New("mcp: tool descriptor carries a prompt-injection pattern")
	// ErrNilResult reports a handler that returned neither a result nor an error.
	ErrNilResult = errors.New("mcp: tool handler returned no result")
	// ErrResultTooLarge reports tool output above MaxResultContentItems or MaxResultTextBytes.
	ErrResultTooLarge = errors.New("mcp: tool result exceeds the sanitizer bound")
)

// HasToolInjection reports a known prompt-injection pattern or a schema too large
// to inspect within MaxToolProperties. An oversized schema fails closed even when
// no injection pattern has been confirmed.
func HasToolInjection(t Tool) bool {
	if len(t.InputSchema.Properties) > MaxToolProperties {
		return true
	}
	if lockdown.HasInjection(t.Description) || lockdown.HasInjection(t.Name) {
		return true
	}
	for _, prop := range t.InputSchema.Properties {
		if lockdown.HasInjection(prop.Description) {
			return true
		}
	}
	return false
}

// SanitizeResult returns a copy of res whose every text item has passed
// lockdown.SanitizePrompt. Tool results carry text the server did not write (transcripts,
// memory facts, package documentation, scanned repository files), and a model reads that
// text in the same context as its instructions, so role delimiters and override phrases
// are neutralized before the result is served. Output above MaxResultContentItems or
// MaxResultTextBytes fails closed with ErrResultTooLarge; res is never modified.
func SanitizeResult(res *ToolResult) (*ToolResult, error) {
	if res == nil {
		return nil, ErrNilResult
	}
	count := len(res.Content)
	if count > MaxResultContentItems {
		return nil, fmt.Errorf("%w: %d content items, maximum %d", ErrResultTooLarge, count, MaxResultContentItems)
	}
	total := 0
	for i := 0; i < count; i++ {
		total += len(res.Content[i].Text)
	}
	if err := checkTextBound(total); err != nil {
		return nil, err
	}
	out := &ToolResult{Content: make([]ContentItem, count), IsError: res.IsError}
	for i := 0; i < count; i++ {
		item := res.Content[i]
		item.Text = lockdown.SanitizePrompt(item.Text)
		out.Content[i] = item
	}
	return out, nil
}

// SanitizeText neutralizes one piece of model-facing text, such as a handler error
// message, under the same MaxResultTextBytes bound as SanitizeResult.
func SanitizeText(text string) (string, error) {
	if err := checkTextBound(len(text)); err != nil {
		return "", err
	}
	return lockdown.SanitizePrompt(text), nil
}

// checkTextBound fails closed when n bytes exceed MaxResultTextBytes.
func checkTextBound(n int) error {
	if n > MaxResultTextBytes {
		return fmt.Errorf("%w: %d bytes of text, maximum %d", ErrResultTooLarge, n, MaxResultTextBytes)
	}
	return nil
}
