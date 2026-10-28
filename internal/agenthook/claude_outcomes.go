package agenthook

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/state"
)

// recordClaudeReturnOutcome appends the subagent outcome row at SubagentStop and folds any
// resolver or append error into Notice so observational failures never alter the hook verdict.
func recordClaudeReturnOutcome(ctx context.Context, client, root string, canonical Canonical, entry correlationEntry, verdict *Verdict) {
	if client != "claude" || entry.Task == "" {
		return
	}
	if err := appendClaudeOutcome(ctx, root, canonical, entry); err != nil {
		verdict.Notice = appendLaneNotice(verdict.Notice, "Claude outcome append failed: "+err.Error())
	}
}

// appendClaudeOutcome is observational: its caller puts any error in Notice, never Deny.
func appendClaudeOutcome(ctx context.Context, root string, canonical Canonical, entry correlationEntry) error {
	if entry.Task == "" {
		return nil
	}
	loc, err := state.ResolveWorkingDir(ctx, root)
	if err != nil {
		return fmt.Errorf("resolve outcome state directory: %w", err)
	}
	tokens, readErr := readClaudeUsage(ctx, canonical.TranscriptPath, canonical.AgentID)
	o := claudeOutcome(entry)
	o.Tokens = tokens
	if readErr != nil {
		o.Note = appendLaneNotice(o.Note, "tokens unread")
	}
	router.PrepareOutcome(&o)
	return router.AppendOutcome(ctx, filepath.Join(loc.RepoDir, router.DefaultOutcomeLogPath), o)
}

func claudeRequestedRoute(raw json.RawMessage) (*router.ClaudeSubagentRoute, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var requested router.ClaudeSubagentRoute
	if err := json.Unmarshal(raw, &requested); err != nil {
		return nil, fmt.Errorf("read requested Claude route: %w", err)
	}
	if requested.Model == "" && requested.Effort == "" {
		return nil, nil
	}
	return &requested, nil
}

func claudeOutcome(entry correlationEntry) router.Outcome {
	var route router.ClaudeSubagentRoute
	if entry.ClaudeSubagent != nil {
		route = *entry.ClaudeSubagent
	}
	if route.Model == "" {
		route.Model = "default"
	}
	resolved := entry.ResolvedModel
	if resolved == "" {
		resolved = "none"
	}
	now := time.Now().UTC()
	var duration int64
	if entry.CreatedAt > 0 {
		duration = max(0, now.Sub(time.Unix(entry.CreatedAt, 0)).Milliseconds())
	}
	result, note := claudeOutcomeResult(entry.HandbackVerdict)
	return router.Outcome{
		Time: now, Task: entry.Task, Lane: "claude-subagent", Target: route.Model,
		Effort: route.Effort, ResolvedModel: resolved, DurationMS: duration,
		Result: result, Note: note, Identity: claudeOutcomeIdentity(entry, resolved),
	}
}

func claudeOutcomeIdentity(entry correlationEntry, resolved string) router.RunIdentity {
	promptDigest := entry.LaunchDigest
	if promptDigest == "" {
		sum := sha256.Sum256([]byte(entry.Task))
		promptDigest = fmt.Sprintf("%x", sum)
	}
	contextDigest := entry.Resolution.ManifestSHA256
	if contextDigest == "" {
		contextDigest = "none"
	}
	return router.RunIdentity{
		PhysicalModel: resolved, Harness: "claude-code", HarnessVersion: "unknown",
		PromptDigest: "sha256:" + promptDigest, ContextDigest: contextDigest,
	}
}
