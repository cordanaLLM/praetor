// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const holdReadTimeout = time.Second

type holdCheck struct {
	entries []config.HoldSettings
	read    func(context.Context, string) ([]byte, error)
}

// newHoldCheck validates and copies holds with expanded paths, returning nil when none are configured.
func newHoldCheck(entries []config.HoldSettings) (*holdCheck, error) {
	if err := config.ValidateHookHolds(entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	check := &holdCheck{entries: make([]config.HoldSettings, len(entries)), read: readHold}
	for i, entry := range entries {
		file, err := expandHoldFile(entry.File)
		if err != nil {
			return nil, err
		}
		check.entries[i] = config.HoldSettings{File: file, Commands: util.CommandNames(entry.Commands), Via: util.CommandNames(entry.Via)}
	}
	return check, config.ValidateHookHolds(check.entries)
}

// expandHoldFile resolves environment and home references, rejecting unresolved hold paths.
func expandHoldFile(file string) (string, error) {
	missing := false
	expanded := os.Expand(file, func(key string) string {
		value, ok := os.LookupEnv(key)
		missing = missing || !ok
		return value
	})
	expanded = homeTarget(expanded)
	if missing || expanded == "" || hasUnresolvedTargetSyntax(expanded) {
		return "", fmt.Errorf("hooks.holds: cannot expand hold file %q", file)
	}
	return filepath.Clean(expanded), nil
}

// judge checks each hold, including indeterminate shell forms only while its file is active.
func (c *holdCheck) judge(ctx context.Context, command, workDir string) Verdict {
	for _, entry := range c.entries {
		if name := guardedHoldCommand(command, entry); name != "" {
			if verdict := c.judgeHeldProgram(ctx, workDir, name, entry); verdict.Outcome != Allow {
				return verdict
			}
		}
	}
	return Verdict{Outcome: Allow}
}

// holdPayload is one script at a bounded shell/eval nesting depth.
type holdPayload struct {
	script string
	depth  int
}

// guardedHoldCommand inspects shell payloads iteratively, bounding depth and total scripts.
func guardedHoldCommand(command string, entry config.HoldSettings) string {
	pending := []holdPayload{{script: command}}
	for at := 0; at < len(pending) && at < admissionMaxSegments; at++ {
		current := pending[at]
		name, payloads := holdScriptPrograms(current.script, entry)
		if name != "" {
			return name
		}
		if len(payloads) > 0 && (current.depth >= 4 || len(pending)+len(payloads) > admissionMaxSegments) {
			return "command exceeds hold payload bound"
		}
		for _, payload := range payloads {
			pending = append(pending, holdPayload{script: payload, depth: current.depth + 1})
		}
	}
	return ""
}

// holdScriptPrograms judges one script and collects literal payloads for the bounded queue.
func holdScriptPrograms(script string, entry config.HoldSettings) (string, []string) {
	commands, reason := holdShellCommands(script)
	if reason != "" {
		return reason, nil
	}
	var payloads []string
	for _, words := range commands {
		name, payload := holdProgram(words, entry)
		if name != "" {
			return name, nil
		}
		if payload != nil {
			payloads = append(payloads, *payload)
		}
	}
	return "", payloads
}

// holdProgram follows literal command words and wrappers, remembering xargs' stdin suffix.
func holdProgram(words []holdWord, entry config.HoldSettings) (string, *string) {
	i := holdPrelude(words)
	throughXargs := false
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		word := words[i]
		if !word.literal {
			return "cannot judge command word " + word.raw, nil
		}
		name := commandName(word.value)
		if holdExempt(name, words[i+1:], entry) {
			return "", nil
		}
		if slices.Contains(entry.Commands, name) {
			return name, nil
		}
		if payload, handled, reason := holdShellPayload(name, words[i+1:]); handled {
			return reason, payload
		}
		throughXargs = throughXargs || name == "xargs"
		next, wrapper, reason := holdWrappedCommand(name, words, i)
		if reason != "" || !wrapper {
			return reason, nil
		}
		i = next
	}
	return holdProgramEnd(i, len(words), throughXargs), nil
}

// holdProgramEnd refuses exhausted wrapper bounds and command words supplied only by xargs.
func holdProgramEnd(i, words int, throughXargs bool) string {
	if i < words {
		return "command exceeds hold wrapper bound"
	}
	if throughXargs {
		return "cannot judge xargs stdin command word"
	}
	return ""
}

// holdExempt recognizes a configured launcher or a lookup that starts no guarded program.
func holdExempt(name string, args []holdWord, entry config.HoldSettings) bool {
	return slices.Contains(entry.Via, name) || holdLookup(name, args)
}

// judgeHeldProgram allows a missing hold file and refuses an existing or unreadable one.
func (c *holdCheck) judgeHeldProgram(ctx context.Context, workDir, name string, entry config.HoldSettings) Verdict {
	file := entry.File
	if !filepath.IsAbs(file) && workDir != "" {
		file = filepath.Join(workDir, file)
	}
	data, err := c.read(ctx, file)
	if errors.Is(err, os.ErrNotExist) {
		return Verdict{Outcome: Allow}
	}
	launcher := strings.Join(entry.Via, ", ") + " launcher is the only path"
	if err != nil {
		return holdRefusal(fmt.Sprintf("hold %s: cannot read: %v; %s", file, err, launcher))
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return holdRefusal(fmt.Sprintf("%s refused: hold %s: %s; %s", name, file, strings.TrimSpace(line), launcher))
}

// holdRefusal returns a denial with a bounded HISS reason.
func holdRefusal(reason string) Verdict {
	return Verdict{Outcome: Deny, Reason: boundReason(refusal("HISS", reason))}
}

// holdReadSlots caps outstanding filesystem reader goroutines across all policies.
var holdReadSlots = make(chan struct{}, 1)

// readHold uses the process-wide slot to bound even uncancelable filesystem reads.
func readHold(ctx context.Context, file string) ([]byte, error) {
	return readHoldWith(ctx, file, contextopt.ReadSnapshot)
}

// readHoldWith acquires the read slot before spawning, retaining it until the reader exits.
func readHoldWith(ctx context.Context, file string, reader func(context.Context, string) ([]byte, error)) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, holdReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case holdReadSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("stalled hold read slot for %s: %w", file, ctx.Err())
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-holdReadSlots }()
		data, err := reader(ctx, file)
		done <- result{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case read := <-done:
		return read.data, read.err
	}
}
