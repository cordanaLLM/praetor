// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// actionUsesLine captures the value of a workflow step's uses: key, trailing comment
	// included. A line that starts with "#" is a comment, never a step.
	actionUsesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(.*?)\s*$`)
	// pinnedActionRef is a remote action pinned by full commit SHA and followed, after at
	// least two spaces as yamllint's comments rule requires, by its release: the form
	// repositories requiring SHA pinning accept and Renovate's github-actions manager keeps
	// current, digest and comment together.
	pinnedActionRef = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[^@\s]+)?)@([0-9a-f]{40}) {2,}# (v?[0-9]+(?:\.[0-9]+)*)$`)
)

// PinnedAction is a remote action reference pinned by full commit SHA with its release as
// a trailing comment, split into its parts.
type PinnedAction struct {
	// Action is the owner/repository[/path] the reference names.
	Action string
	// SHA is the 40-hex lowercase commit the reference runs.
	SHA string
	// Release is the release tag the comment names, such as "v7.0.1".
	Release string
}

// ActionUsesValue returns the value of the uses: key that one workflow line declares, with
// any trailing comment, and whether the line declares one.
func ActionUsesValue(line string) (string, bool) {
	match := actionUsesLine.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// ParsePinnedAction splits a uses: value (ActionUsesValue) of the form
// owner/repository[/path]@<40-hex SHA>  # <release>. ok is false for any other form: a tag,
// a branch, a short or uppercase SHA, a SHA without its release comment or with a comment
// one space from it, a local action or a Docker reference.
func ParsePinnedAction(ref string) (PinnedAction, bool) {
	match := pinnedActionRef.FindStringSubmatch(ref)
	if match == nil {
		return PinnedAction{}, false
	}
	return PinnedAction{Action: match[1], SHA: match[2], Release: match[3]}, true
}

// ActionUse is one workflow line that declares a uses: key.
type ActionUse struct {
	// Line is the zero-based index of the line in the workflow.
	Line int
	// Ref is the uses: value, trailing comment included (ActionUsesValue).
	Ref string
	// Pin is Ref split into its parts; Pinned reports whether Ref has the SHA-pinned form.
	Pin    PinnedAction
	Pinned bool
}

// ScanActionUses splits workflow into lines and returns them with every line that declares a
// uses: key, in order. It refuses a workflow of more than maxLines lines (HISS-02); each
// caller passes the bound that fits the workflows it reads.
func ScanActionUses(workflow string, maxLines int) ([]string, []ActionUse, error) {
	lines := strings.Split(workflow, "\n")
	if len(lines) > maxLines {
		return nil, nil, fmt.Errorf("workflow has %d lines, want at most %d", len(lines), maxLines)
	}
	var uses []ActionUse
	for index := 0; index < len(lines) && index < maxLines; index++ {
		ref, declared := ActionUsesValue(lines[index])
		if !declared {
			continue
		}
		pin, pinned := ParsePinnedAction(ref)
		uses = append(uses, ActionUse{Line: index, Ref: ref, Pin: pin, Pinned: pinned})
	}
	return lines, uses, nil
}
