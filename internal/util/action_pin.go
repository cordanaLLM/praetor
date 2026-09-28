// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import "regexp"

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
