// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

const backgroundMessage = "bare-background rule (hooks.background.enforce): use client's background-task mechanism or systemd user unit; "

// Neighbours exclude compound operators, fd duplication and case fall-through. &>
// is exempt only inside a literal bash/zsh payload; the caller's shell is unknown.
var backgroundOperators = regexp.MustCompile(`(?:^|[^&<>|;])&(?:[^&]|$)`)
var backgroundBashOperators = regexp.MustCompile(`(?:^|[^&<>|;])&(?:[^&>]|$)`)

type backgroundScript struct {
	source, shell string
	depth         int
}

// shellPayloadReader isolates the payload step from the background policy.
// TODO(PR 1958): replace readBackgroundPayloads with an adapter to the shared
// holdShellCommands/holdShellPayload walker once both branches land. Preserve its
// literal-word and ambiguity contract; no second hold-word parser belongs here.
type shellPayloadReader func(string) ([]backgroundScript, string)

// backgroundRefusal leaves disabled enforcement out of shell parsing and input bounds.
func (p *Policy) backgroundRefusal(command string) string {
	if !p.background {
		return ""
	}
	return backgroundReason(command, readBackgroundPayloads)
}

// backgroundBounds runs before parsing or joining, so neither allocation nor continuations
// can hide oversized inputs. MaxSourceBytes shares the hold guard's input ceiling.
func backgroundBounds(command string) string {
	if len(command) > contextopt.MaxSourceBytes {
		return "command unreadable: background byte bound"
	}
	if strings.Count(command, "\n") >= admissionMaxLines {
		return "command unreadable: background physical-line bound"
	}
	return ""
}

// backgroundReason uses a bounded work queue rather than recursive shell evaluation.
// Every source scope and literal payload is inspected; dynamic payloads fail closed.
func backgroundReason(command string, payloads shellPayloadReader) string {
	queue := []backgroundScript{{source: command}}
	budget := 0
	for i := 0; i < len(queue) && i < admissionMaxSegments; i++ {
		script := queue[i]
		if reason := backgroundBounds(script.source); reason != "" {
			return reason
		}
		if script.depth > admissionMaxWrappers {
			return "command unreadable: background inspection depth bound"
		}
		budget += len(script.source)
		if budget > contextopt.MaxSourceBytes*admissionMaxWrappers {
			return "command unreadable: background inspection byte bound"
		}
		next, reason := inspectBackgroundScript(script, payloads)
		if reason != "" {
			return reason
		}
		queue = append(queue, next...)
		if len(queue) > admissionMaxSegments {
			return "command unreadable: background inspection scope bound"
		}
	}
	return ""
}

// inspectBackgroundScript shares PR 1958's comment-aware joiner and the HISS lexer.
// Arithmetic operators are blanked by that lexer, while nested executable code remains.
func inspectBackgroundScript(script backgroundScript, payloads shellPayloadReader) ([]backgroundScript, string) {
	code, sources, ok := hiss.ShellInspection(hiss.JoinShellContinuations(script.source), admissionMaxLines)
	if !ok {
		return nil, "command unreadable"
	}
	pattern := backgroundOperators
	if script.shell == "bash" || script.shell == "zsh" {
		pattern = backgroundBashOperators
	}
	if pattern.MatchString(code) {
		return nil, "bare & prohibited"
	}
	var next []backgroundScript
	for _, source := range sources {
		children, reason := payloads(source)
		if reason != "" {
			return nil, reason
		}
		for _, child := range children {
			child.depth = script.depth + 1
			if child.shell == "eval" {
				child.shell = script.shell
			}
			next = append(next, child)
		}
	}
	return next, ""
}
