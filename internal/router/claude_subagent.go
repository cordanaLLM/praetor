package router

import (
	"fmt"
	"slices"
)

// MaxClaudeSubagentRoutes bounds the per-dispatch task override table.
const MaxClaudeSubagentRoutes = MaxRoutingTags

func validateClaudeSubagent(cfg *RoutingConfig) error {
	if len(cfg.ClaudeSubagent) > MaxClaudeSubagentRoutes {
		return fmt.Errorf("%w: claude_subagent exceeds %d entries", ErrInvalidRoutingConfig, MaxClaudeSubagentRoutes)
	}
	declared := DeclaredTaskLabels(cfg)
	for label, route := range cfg.ClaudeSubagent {
		if !slices.Contains(declared, label) {
			return fmt.Errorf("%w: claude_subagent key %q is not declared by target_tasks", ErrInvalidRoutingConfig, label)
		}
		if !slices.Contains([]string{"haiku", "sonnet", "opus", "fable"}, route.Model) {
			return fmt.Errorf("%w: claude_subagent key %q has invalid model %q", ErrInvalidRoutingConfig, label, route.Model)
		}
		if !slices.Contains([]string{"", "low", "medium", "high", "xhigh", "max"}, route.Effort) {
			return fmt.Errorf("%w: claude_subagent key %q has invalid effort %q", ErrInvalidRoutingConfig, label, route.Effort)
		}
	}
	return nil
}
