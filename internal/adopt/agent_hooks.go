package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
)

func reconcileAgentHooks(ctx context.Context, s *adoptSession) error {
	reason := "These clients have no PreToolUse hook surface today"
	s.report.recordNotApplicable("cursor", reason)
	s.report.recordNotApplicable("windsurf", reason)
	s.report.recordNotApplicable("copilot", reason)

	for _, client := range []string{"claude", "codex", "gemini"} {
		if err := reconcileClientHook(s, client); err != nil {
			return err
		}
	}
	return nil
}

func reconcileClientHook(s *adoptSession, client string) error {
	var relPath string
	var unit time.Duration
	switch client {
	case "claude":
		relPath = ".claude/settings.json"
		unit = time.Second
	case "codex":
		relPath = ".codex/hooks.json"
		unit = time.Second
	case "gemini":
		relPath = ".gemini/settings.json"
		unit = time.Millisecond
	default:
		return nil
	}

	fullPath, err := repoFile(s.repoPath, relPath)
	if err != nil {
		return err
	}

	if !fileExists(fullPath) {
		s.report.recordSkipped(relPath, "Hook configuration file does not exist")
		return nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", relPath, err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", relPath, err)
	}

	hooksMap, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	changed := false
	for _, row := range agenthook.Registrations(client) {
		if row.Event != agenthook.EventPreTool {
			continue
		}
		if injectHook(hooksMap, row, unit) {
			changed = true
		}
	}

	if !changed {
		s.report.recordReconciled(relPath, "PreToolUse interceptor already registered")
		return nil
	}

	if s.opts.DryRun {
		return nil
	}

	bakPath := fullPath + ".bak"
	if err := os.WriteFile(bakPath, data, filePerm); err != nil {
		return fmt.Errorf("backup %s: %w", relPath, err)
	}

	out, err := marshalJSONWithIndent(root)
	if err != nil {
		return fmt.Errorf("encode %s: %w", relPath, err)
	}

	if err := s.write(fullPath, out, filePerm); err != nil {
		return err
	}

	s.report.recordReconciled(relPath, "Registered PreToolUse anti-evasion interceptor")
	return nil
}

func injectHook(hooksMap map[string]any, row agenthook.Registration, unit time.Duration) bool {
	changed := false
	nativeEvent := row.NativeEvent
	matcher := row.Matcher
	if matcher == "" {
		matcher = "*"
	}

	eventList, ok := hooksMap[nativeEvent].([]any)
	if !ok {
		return false
	}

	for _, rawGroup := range eventList {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}

		gMatcher, _ := group["matcher"].(string)
		if gMatcher != matcher {
			continue
		}

		hooksList, ok := group["hooks"].([]any)
		if !ok {
			continue
		}

		if appendIfNotExists(&hooksList, row, unit) {
			group["hooks"] = hooksList
			changed = true
		}
	}
	return changed
}

func appendIfNotExists(hooksList *[]any, row agenthook.Registration, unit time.Duration) bool {
	targetCmd := row.Command()
	for _, rawHook := range *hooksList {
		hookObj, ok := rawHook.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hookObj["command"].(string)
		if cmd == targetCmd {
			return false
		}
	}

	newHook := map[string]any{
		"type":    "command",
		"command": targetCmd,
	}
	if row.Timeout > 0 {
		newHook["timeout"] = int(row.Timeout / unit)
	}
	*hooksList = append(*hooksList, newHook)
	return true
}

func marshalJSONWithIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder adds a trailing newline, which is standard for JSON files.
	return buf.Bytes(), nil
}
