// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxPrePushBytes     = 1 << 20
	maxPrePushLines     = 4096
	maxPrePushLineBytes = 4096
	defaultRemoteName   = "origin"
)

// PrePushUpdate represents one non-delete ref update line from git's pre-push stdin protocol.
type PrePushUpdate struct {
	LocalRef  string
	LocalOID  string
	RemoteRef string
	RemoteOID string
}

// DefaultBaseRef resolves the remote default branch reference (<remote>/<branch>) via forge.RepositoryDefaultBranch.
func DefaultBaseRef(ctx context.Context, repoDir, remote string) (string, error) {
	if remote == "" {
		remote = defaultRemoteName
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, repoDir, nil)
	if err != nil {
		return "", fmt.Errorf("resolve default branch: %w", err)
	}
	return remote + "/" + branch, nil
}

// ParsePrePushStdin reads and validates git pre-push ref lines from r, skipping ref deletions.
func ParsePrePushStdin(r io.Reader) ([]PrePushUpdate, error) {
	if r == nil {
		return nil, errors.New("pre-push stdin reader is nil")
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxPrePushBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read pre-push stdin: %w", err)
	}
	if len(raw) > maxPrePushBytes {
		return nil, fmt.Errorf("pre-push stdin exceeds %d bytes", maxPrePushBytes)
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxPrePushLines {
		return nil, fmt.Errorf("pre-push stdin exceeds %d lines", maxPrePushLines)
	}
	updates := make([]PrePushUpdate, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		u, err := parsePrePushLine(lines[i])
		if err != nil {
			return nil, err
		}
		if !isZeroOID(u.LocalOID) {
			updates = append(updates, u)
		}
	}
	return updates, nil
}

// parsePrePushLine parses one "<local ref> <local oid> <remote ref> <remote oid>" line.
func parsePrePushLine(line string) (PrePushUpdate, error) {
	if len(line) > maxPrePushLineBytes {
		return PrePushUpdate{}, fmt.Errorf("pre-push stdin line exceeds %d bytes", maxPrePushLineBytes)
	}
	fields := strings.Fields(line)
	if len(fields) != 4 || !isValidHexOID(fields[1]) || !isValidHexOID(fields[3]) {
		return PrePushUpdate{}, fmt.Errorf("malformed pre-push ref input %q: expected <local-ref> <local-oid> <remote-ref> <remote-oid>", line)
	}
	return PrePushUpdate{
		LocalRef:  fields[0],
		LocalOID:  fields[1],
		RemoteRef: fields[2],
		RemoteOID: fields[3],
	}, nil
}

// isValidHexOID reports whether oid is a 40- or 64-character hexadecimal object ID
// (SHA-1 or SHA-256 repositories).
func isValidHexOID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	_, err := hex.DecodeString(oid)
	return err == nil
}

// isZeroOID reports whether oid consists entirely of '0' characters.
func isZeroOID(oid string) bool {
	if len(oid) == 0 {
		return false
	}
	for i := 0; i < len(oid); i++ {
		if oid[i] != '0' {
			return false
		}
	}
	return true
}

// ResolvePrePushBase resolves the base commit OID for a pushed update or returns a skip reason.
func ResolvePrePushBase(ctx context.Context, repoDir, remote, localOID, remoteOID string) (string, string) {
	if !isZeroOID(remoteOID) && commitExistsLocally(ctx, repoDir, remoteOID) {
		return remoteOID, ""
	}
	defaultRef, err := DefaultBaseRef(ctx, repoDir, remote)
	if err != nil {
		return "", err.Error()
	}
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	out, err := util.RunGit(gitCtx, repoDir, "merge-base", localOID, defaultRef)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Sprintf("no merge base with %s", defaultRef)
	}
	return strings.TrimSpace(out), ""
}

// commitExistsLocally reports whether oid is present as a commit object in repoDir.
func commitExistsLocally(ctx context.Context, repoDir, oid string) bool {
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	_, err := util.RunGit(gitCtx, repoDir, "cat-file", "-e", oid+"^{commit}")
	return err == nil
}
