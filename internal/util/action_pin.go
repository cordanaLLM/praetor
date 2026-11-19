// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	// shaPinnedActionRef is a remote action pinned by full commit SHA, with or without a
	// trailing comment. Any run of spaces or tabs may separate the comment from the SHA:
	// Renovate's github-actions manager and pinact write one space, yamllint's default
	// comments rule only warns about it, and two or more are as valid (#610).
	shaPinnedActionRef = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[^@\s]+)?)@([0-9a-f]{40})(?:[ \t]+#(.*))?$`)
	// releaseComment is a trailing comment that names a release, such as " v7.0.1": the
	// form repositories requiring SHA pinning accept and pinning tools keep current, digest
	// and comment together.
	releaseComment = regexp.MustCompile(`^[ \t]+(v?[0-9]+(?:\.[0-9]+)*)$`)
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
	_, uses, err := ScanActionUses(line, 1)
	if err != nil || len(uses) != 1 {
		return "", false
	}
	return uses[0].Ref, true
}

// ParseSHAPin splits a uses: value (ActionUsesValue) pinned by full commit SHA, of the form
// owner/repository[/path]@<40-hex SHA>, optionally followed by whitespace and a "#" comment.
// Release is the release the comment names (releaseComment), or empty when there is no
// comment or it names none. ok is false for any other form: a tag, a branch, a short or
// uppercase SHA, a local action or a Docker reference.
func ParseSHAPin(ref string) (PinnedAction, bool) {
	match := shaPinnedActionRef.FindStringSubmatch(ref)
	if match == nil {
		return PinnedAction{}, false
	}
	pin := PinnedAction{Action: match[1], SHA: match[2]}
	if release := releaseComment.FindStringSubmatch(match[3]); release != nil {
		pin.Release = release[1]
	}
	return pin, true
}

// ParsePinnedAction splits a uses: value (ActionUsesValue) of the form
// owner/repository[/path]@<40-hex SHA> # <release>, with any run of spaces or tabs on
// either side of the "#" (ParseSHAPin). ok is false for any other form, a SHA without its
// release comment or with a comment that names no release included.
func ParsePinnedAction(ref string) (PinnedAction, bool) {
	pin, ok := ParseSHAPin(ref)
	if !ok || pin.Release == "" {
		return PinnedAction{}, false
	}
	return pin, true
}

// ActionUse is one structurally parsed mapping value under a uses key.
type ActionUse struct {
	// Line is the zero-based index of the line in the workflow.
	Line int
	// Column is the one-based YAML source column of the value.
	Column int
	// Start and End delimit the original scalar in workflow bytes, excluding its comment.
	Start, End int
	// Value is the decoded YAML scalar, excluding its trailing comment.
	Value string
	// Ref is the uses: value, trailing comment included (ActionUsesValue).
	Ref string
	// Pin is Ref split into its parts when SHAPinned: Ref is pinned by full commit SHA
	// (ParseSHAPin). Pinned reports whether Ref also carries its release comment, the
	// SHA-pinned form ParsePinnedAction accepts.
	Pin       PinnedAction
	SHAPinned bool
	Pinned    bool
	// source retains scalar properties for a rewrite that preserves anchors and tags.
	source *yaml.Node
}

// Replacement encodes a new value with the parsed scalar's anchors and tags, without comments.
func (use ActionUse) Replacement(value string) (string, error) {
	if use.source == nil {
		return "", fmt.Errorf("uses value has no parsed YAML source")
	}
	node := *use.source
	node.Value = value
	node.Style &= yaml.TaggedStyle
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		return "", fmt.Errorf("encode uses value: %w", err)
	}
	return strings.TrimSuffix(string(encoded), "\n"), nil
}

// ScanActionUses splits workflow into lines and returns them with every line that declares a
// uses: key, in order. It refuses a workflow of more than maxLines lines (HISS-02); each
// caller passes the bound that fits the workflows it reads. An optional reserved placeholder
// is allowed only in non-uses scalar mapping values, after decoding with the same YAML reader.
func ScanActionUses(workflow string, maxLines int, placeholders ...string) ([]string, []ActionUse, error) {
	source := newActionSource(workflow)
	if len(source.lines) > maxLines {
		return nil, nil, fmt.Errorf("workflow has %d lines, want at most %d", len(source.lines), maxLines)
	}
	var root yaml.Node
	if err := DecodeYAMLDocument([]byte(workflow), &root, YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return nil, nil, err
	}
	uses, err := walkActionUses(source, &root)
	if err != nil {
		return nil, nil, err
	}
	if err := validateActionPlaceholders(&root, placeholders, workflow); err != nil {
		return nil, nil, err
	}
	return source.lines, uses, nil
}
