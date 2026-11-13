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
	"github.com/cordanaLLM/praetor/internal/hiss"
)

const holdReadTimeout = time.Second

type holdCheck struct {
	entries []config.HoldSettings
}

func newHoldCheck(entries []config.HoldSettings) (*holdCheck, error) {
	if err := config.ValidateHookHolds(entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	check := &holdCheck{entries: make([]config.HoldSettings, len(entries))}
	for i, entry := range entries {
		file, err := expandHoldFile(entry.File)
		if err != nil {
			return nil, err
		}
		check.entries[i] = config.HoldSettings{File: file, Commands: slices.Clone(entry.Commands), Via: slices.Clone(entry.Via)}
	}
	return check, nil
}

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

func (c *holdCheck) judge(ctx context.Context, command, workDir string) Verdict {
	lines, ok := hiss.ShellCode(command, admissionMaxLines)
	if !ok {
		return holdRefusal("hooks.holds: command unreadable; split command or write it to file first")
	}
	segments := strings.Split(segmentSeparators.Replace(strings.Join(lines, "\n")), "\n")
	if len(segments) > admissionMaxSegments {
		return holdRefusal("hooks.holds: too many simple commands; split command")
	}
	for _, entry := range c.entries {
		if name := guardedHoldCommand(segments, entry); name != "" {
			if verdict := judgeHeldProgram(ctx, workDir, name, entry); verdict.Outcome != Allow {
				return verdict
			}
		}
	}
	return Verdict{Outcome: Allow}
}

func guardedHoldCommand(segments []string, entry config.HoldSettings) string {
	for _, segment := range segments {
		if name := holdProgram(strings.Fields(segment), entry); name != "" {
			return name
		}
	}
	return ""
}

func holdProgram(words []string, entry config.HoldSettings) string {
	if len(words) > admissionMaxWords {
		return "command exceeds hold word bound"
	}
	i := skipPrelude(words)
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		name := commandName(words[i])
		if slices.Contains(entry.Via, name) {
			return ""
		}
		if slices.Contains(entry.Commands, name) {
			return name
		}
		spec, wrapper := holdWrapper(name)
		if !wrapper {
			return ""
		}
		i = commandAfter(words, i, spec)
	}
	if i < len(words) {
		return "command exceeds hold wrapper bound"
	}
	return ""
}

// Admission refuses sudo before unwrapping it. Holds inspect its target first so
// a held command's refusal can name the hold; other privilege checks still run.
func holdWrapper(name string) (wrapperSpec, bool) {
	if name == "sudo" {
		return wrapperSpec{values: []string{"-C", "--close-from", "-D", "--chdir", "-g", "--group", "-h", "--host",
			"-p", "--prompt", "-R", "--chroot", "-T", "--command-timeout", "-U", "--other-user", "-u", "--user"}}, true
	}
	spec, ok := admissionWrappers[name]
	return spec, ok
}

func judgeHeldProgram(ctx context.Context, workDir, name string, entry config.HoldSettings) Verdict {
	file := entry.File
	if !filepath.IsAbs(file) && workDir != "" {
		file = filepath.Join(workDir, file)
	}
	data, err := readHold(ctx, file)
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

func holdRefusal(reason string) Verdict {
	return Verdict{Outcome: Deny, Reason: boundReason(refusal("HISS", reason))}
}

// readHold reuses stable, regular-file snapshots (1 MiB cap). The outer deadline
// also bounds a filesystem open that stalls before snapshot's context checks.
func readHold(ctx context.Context, file string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, holdReadTimeout)
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := contextopt.ReadSnapshot(ctx, file)
		done <- result{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case read := <-done:
		return read.data, read.err
	}
}
