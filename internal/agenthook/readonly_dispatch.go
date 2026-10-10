package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// claudeContextCap is the cap Claude Code applies to a hook's additionalContext: a longer
// value is saved to a file and replaced by its path and a 2,000-character preview, which the
// agent is not asked to read (code.claude.com/docs/en/hooks, JSON output).
const claudeContextCap = 10000

// readOnlyRewriteNotice names the rewrite on the hook's own output.
const readOnlyRewriteNotice = "read-only dispatch: prompt prefixed with the " + agentcontext.CanonicalReadOnlyFile + " projection"

// readOnlyBriefs reports, per brief, whether its agent runs read-only: the brief's readonly
// field, or a read-only agent type its dispatch names (agentcontext.IsReadOnlyRole); and
// whether any brief does.
func readOnlyBriefs(canonical Canonical) ([]bool, bool) {
	marked := make([]bool, len(canonical.Briefs))
	anyMarked := false
	for index := 0; index < len(canonical.Briefs) && index < MaxDispatchBriefs; index++ {
		marked[index] = caveman.IsBriefReadOnly(canonical.Briefs[index]) || readOnlyRole(rolesOf(canonical, index))
		anyMarked = anyMarked || marked[index]
	}
	return marked, anyMarked
}

func rolesOf(canonical Canonical, index int) []string {
	if index < len(canonical.Roles) {
		return canonical.Roles[index]
	}
	return nil
}

func readOnlyRole(roles []string) bool {
	for _, role := range roles {
		if agentcontext.IsReadOnlyRole(role) {
			return true
		}
	}
	return false
}

// readOnlyDelivery returns the allow of a validated dispatch, and the prompt a Claude dispatch
// was rewritten to. A read-write dispatch is a plain allow. A read-only one gets its dispatch
// input rewritten so each read-only brief opens with the banner and the compiled projection:
// Claude's updatedInput, which replaces the whole tool input, and agy's overwrite, a shallow
// merge of the Subagents array. Codex and Gemini expose no channel that reaches the subagent,
// so their allow names the substitution instead. A projection that cannot be read is an error:
// a read-only agent never runs on the full context unannounced.
func readOnlyDelivery(ctx context.Context, client, root string, canonical Canonical) (Verdict, string, error) {
	marked, anyMarked := readOnlyBriefs(canonical)
	if !anyMarked {
		return Verdict{Outcome: Allow}, "", nil
	}
	if client != "claude" && client != "agy" {
		return Verdict{Outcome: Allow, Notice: "read-only projection not delivered: " + client +
			" has no channel that reaches the subagent; it loads the full context, the brief's readonly field its only read-only signal"}, "", nil
	}
	projection, err := loadReadOnlyProjection(ctx, root)
	if err != nil {
		return Verdict{}, "", err
	}
	if client == "agy" {
		overwrite, err := agyReadOnlyOverwrite(canonical, marked, projection)
		return Verdict{Outcome: Allow, UpdatedInput: overwrite, Notice: readOnlyRewriteNotice}, "", err
	}
	prompt := readOnlyPrompt(projection, canonical.Briefs[0])
	input, err := claudeReadOnlyInput(canonical.DispatchInput, prompt)
	return Verdict{Outcome: Allow, UpdatedInput: input, Notice: readOnlyRewriteNotice}, prompt, err
}

// loadReadOnlyProjection reads the compiled AGENTS.readonly.md at root through the confined
// read and refuses one that is absent, unreadable or not a read-only projection.
func loadReadOnlyProjection(ctx context.Context, root string) (string, error) {
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, agentcontext.CanonicalReadOnlyFile)
	if err == nil && !exists {
		err = errors.New("file absent")
	}
	if err != nil {
		return "", fmt.Errorf("read-only dispatch needs %s: %w; run praetorctl compile-context", agentcontext.CanonicalReadOnlyFile, err)
	}
	if !strings.Contains(string(data), agentcontext.ReadOnlyBanner) {
		return "", fmt.Errorf("read-only dispatch needs %s: file lacks the read-only banner; run praetorctl compile-context", agentcontext.CanonicalReadOnlyFile)
	}
	return string(data), nil
}

// readOnlyPrompt opens brief with the read-only banner and the projection.
func readOnlyPrompt(projection, brief string) string {
	return agentcontext.ReadOnlyBanner + "\n\n<read-only-context file=\"" + agentcontext.CanonicalReadOnlyFile + "\">\n" +
		strings.TrimRight(projection, "\n") + "\n</read-only-context>\n\n" + brief
}

// claudeReadOnlyInput returns the whole Claude tool input with prompt in place of the brief;
// updatedInput replaces the input, so every other field comes back unchanged.
func claudeReadOnlyInput(raw json.RawMessage, prompt string) (json.RawMessage, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil || input == nil {
		return nil, errors.New("tool_input must be an object")
	}
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return nil, fmt.Errorf("encode read-only prompt: %w", err)
	}
	input["prompt"] = encoded
	return json.Marshal(input)
}

// agyReadOnlyOverwrite returns agy's overwrite object: the whole Subagents array, since the
// merge replaces a top-level key wholesale, with each read-only subagent's Prompt rewritten and
// every other key and subagent unchanged.
func agyReadOnlyOverwrite(canonical Canonical, marked []bool, projection string) (json.RawMessage, error) {
	var args struct {
		Subagents []map[string]json.RawMessage `json:"Subagents"`
	}
	if err := json.Unmarshal(canonical.DispatchInput, &args); err != nil || len(args.Subagents) != len(canonical.Briefs) {
		return nil, errors.New("toolCall.args.Subagents changed shape after decoding")
	}
	for index := 0; index < len(args.Subagents) && index < MaxDispatchBriefs; index++ {
		if !marked[index] {
			continue
		}
		encoded, err := json.Marshal(readOnlyPrompt(projection, canonical.Briefs[index]))
		if err != nil {
			return nil, fmt.Errorf("encode read-only prompt: %w", err)
		}
		args.Subagents[index]["Prompt"] = encoded
	}
	return json.Marshal(map[string]any{"Subagents": args.Subagents})
}

// launchVerdict judges a Claude dispatch receipt against the pending row it bound: a read-only
// dispatch whose agent launched with any prompt but the rewritten one means the client refused
// the input change, and is reported to the parent, never passed silently.
func launchVerdict(pending correlationEntry, canonical Canonical) Verdict {
	if pending.LaunchDigest == "" {
		return Verdict{Outcome: Allow}
	}
	if len(canonical.Briefs) == 1 && correlationLaunchDigest(canonical.Briefs[0]) == pending.LaunchDigest {
		return Verdict{Outcome: Allow}
	}
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] read-only projection not delivered: agent " + canonical.AgentID +
		" launched without the rewritten prompt (the client refused updatedInput) and runs on the full context; stop it and dispatch again"}
}

// evaluateSubagentStart hands a starting agent of a read-only type the projection as
// additionalContext. SubagentStart cannot block, so a projection that cannot be read, or one
// over the client's context cap, is replaced by the banner and the substitution named.
func evaluateSubagentStart(ctx context.Context, canonical Canonical, root string) Verdict {
	if !agentcontext.IsReadOnlyRole(canonical.AgentType) {
		return Verdict{Outcome: Allow}
	}
	projection, err := loadReadOnlyProjection(ctx, root)
	if err != nil {
		return Verdict{Outcome: Allow, AddedContext: agentcontext.ReadOnlyBanner + "\n\nRead-only projection not delivered: " + err.Error()}
	}
	if utf8.RuneCountInString(projection) > claudeContextCap {
		return Verdict{Outcome: Allow, AddedContext: fmt.Sprintf("%s\n\nRead-only projection not delivered whole: it exceeds the %d-character context cap; read %s before acting.",
			agentcontext.ReadOnlyBanner, claudeContextCap, filepath.Join(root, agentcontext.CanonicalReadOnlyFile))}
	}
	return Verdict{Outcome: Allow, AddedContext: projection}
}
