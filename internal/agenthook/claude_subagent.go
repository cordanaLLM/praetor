package agenthook

import (
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/router"
)

// claudeVerdictWord is called only after the handback passed its register contract.
func claudeVerdictWord(text string) string {
	word, err := caveman.ExtractReturnVerdict(text)
	if err != nil {
		return ""
	}
	if len(word) > 64 {
		return "oversized"
	}
	return word
}

func claudeOutcomeResult(word string) (string, string) {
	switch word {
	case "pass", "green", "ok", "done":
		return router.OutcomeOK, ""
	case "fail", "red", "blocked", "partial":
		return router.OutcomeFail, ""
	default:
		if word == "" {
			word = "none"
		}
		return router.OutcomeFail, "verdict " + word
	}
}

// routeClaudeSubagent composes a validated task override with any prompt rewrite.
// Presence of either native override key keeps both choices with the caller.
func routeClaudeSubagent(routes map[string]router.ClaudeSubagentRoute, canonical Canonical, verdict *Verdict) (*router.ClaudeSubagentRoute, error) {
	label, err := caveman.ExtractBriefTask(canonical.Briefs[0])
	if err != nil {
		return nil, err
	}
	route, found := routes[label]
	if !found {
		return nil, nil
	}
	raw := canonical.DispatchInput
	if verdict.UpdatedInput != nil {
		raw = verdict.UpdatedInput
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decode Claude dispatch input: %w", err)
	}
	if input == nil {
		return nil, fmt.Errorf("claude dispatch input must be an object")
	}
	_, modelNamed := input["model"]
	_, effortNamed := input["effort"]
	if modelNamed || effortNamed {
		return nil, nil
	}
	updated, err := claudeRoutedInput(input, route)
	if err != nil {
		return nil, err
	}
	verdict.UpdatedInput = updated
	verdict.Notice = appendLaneNotice(verdict.Notice, fmt.Sprintf("routed %s -> %s/%s", label, route.Model, route.Effort))
	return &route, nil
}

// claudeRoutedInput preserves unknown tool fields and omits an unspecified effort.
func claudeRoutedInput(input map[string]json.RawMessage, route router.ClaudeSubagentRoute) (json.RawMessage, error) {
	encoded, err := json.Marshal(route)
	if err != nil {
		return nil, fmt.Errorf("encode Claude route: %w", err)
	}
	if err := json.Unmarshal(encoded, &input); err != nil {
		return nil, fmt.Errorf("merge Claude route: %w", err)
	}
	updated, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode Claude dispatch input: %w", err)
	}
	return updated, nil
}
