// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxPrePushBytes     = 1 << 20
	maxPrePushLines     = 4096
	maxPrePushLineBytes = 4096
	maxLsRemoteLines    = 64
	prePushReadTimeout  = 30 * time.Second
	defaultRemoteName   = "origin"
)

// PrePushUpdate represents one non-delete ref update line from git's pre-push stdin protocol.
type PrePushUpdate struct {
	LocalRef  string
	LocalOID  string
	RemoteRef string
	RemoteOID string
}

// DefaultBaseRef resolves the remote default branch reference (<remote>/<branch>) by asking the
// remote first via `git ls-remote --symref <remote> HEAD` and falling back to
// forge.RepositoryDefaultBranch when the remote cannot answer. The second return value names
// which answer was used ("remote" or "local").
func DefaultBaseRef(ctx context.Context, repoDir, remote string) (string, string, error) {
	if ctx == nil {
		return "", "", errors.New("resolving default base ref requires a context")
	}
	if remote == "" {
		remote = defaultRemoteName
	}
	if branch := remoteDefaultBranch(ctx, repoDir, remote); branch != "" {
		return remote + "/" + branch, "remote", nil
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, repoDir, nil)
	if err != nil {
		return "", "", fmt.Errorf("resolve default branch: %w", err)
	}
	return remote + "/" + branch, "local", nil
}

// remoteDefaultBranch queries <remote>'s symbolic HEAD via `git ls-remote --symref <remote> HEAD`,
// matching the Python pre-push hook's remote_default_ref. It returns "" when the remote cannot
// answer or reports no symbolic HEAD ref within maxLsRemoteLines.
func remoteDefaultBranch(ctx context.Context, repoDir, remote string) string {
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	out, err := util.RunGit(gitCtx, repoDir, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return ""
	}
	return parseRemoteDefaultBranch(out)
}

// parseRemoteDefaultBranch extracts the branch name from `git ls-remote --symref` output,
// inspecting at most maxLsRemoteLines lines.
func parseRemoteDefaultBranch(out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	limit := min(len(lines), maxLsRemoteLines)
	for i := 0; i < limit; i++ {
		fields := strings.Fields(lines[i])
		if len(fields) >= 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			if branch, ok := strings.CutPrefix(fields[1], "refs/heads/"); ok && branch != "" {
				return branch
			}
		}
	}
	return ""
}

// ParsePrePushStdin reads and validates git pre-push ref lines from r under ctx, skipping ref deletions.
func ParsePrePushStdin(ctx context.Context, r io.Reader) ([]PrePushUpdate, error) {
	raw, err := readPrePushBytes(ctx, r)
	if err != nil {
		return nil, err
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

// readPrePushBytes reads at most maxPrePushBytes+1 bytes from r while obeying ctx's deadline.
func readPrePushBytes(ctx context.Context, r io.Reader) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("pre-push stdin requires a context")
	}
	if r == nil {
		return nil, errors.New("pre-push stdin reader is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, prePushReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read pre-push stdin timed out: %w", err)
	}
	type readResult struct {
		raw []byte
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		raw, err := io.ReadAll(io.LimitReader(r, maxPrePushBytes+1))
		done <- readResult{raw: raw, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("read pre-push stdin timed out: %w", ctx.Err())
	case res := <-done:
		if res.err != nil {
			return nil, fmt.Errorf("read pre-push stdin: %w", res.err)
		}
		if len(res.raw) > maxPrePushBytes {
			return nil, fmt.Errorf("pre-push stdin exceeds %d bytes", maxPrePushBytes)
		}
		return res.raw, nil
	}
}

// parsePrePushLine parses one "<local ref> <local oid> <remote ref> <remote oid>" line.
func parsePrePushLine(line string) (PrePushUpdate, error) {
	if len(line) > maxPrePushLineBytes {
		return PrePushUpdate{}, fmt.Errorf("pre-push stdin line exceeds %d bytes", maxPrePushLineBytes)
	}
	fields := strings.Fields(line)
	if len(fields) != 4 || !util.ValidGitObjectID(fields[1]) || !util.ValidGitObjectID(fields[3]) {
		return PrePushUpdate{}, fmt.Errorf("malformed pre-push ref input %q: expected <local-ref> <local-oid> <remote-ref> <remote-oid>", line)
	}
	return PrePushUpdate{
		LocalRef:  fields[0],
		LocalOID:  fields[1],
		RemoteRef: fields[2],
		RemoteOID: fields[3],
	}, nil
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
	if !isZeroOID(remoteOID) && isAncestorCommit(ctx, repoDir, remoteOID, localOID) {
		return remoteOID, ""
	}
	defaultRef, source, err := DefaultBaseRef(ctx, repoDir, remote)
	if err != nil {
		return "", err.Error()
	}
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	out, err := util.RunGit(gitCtx, repoDir, "merge-base", localOID, defaultRef)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Sprintf("no merge base with %s (%s)", defaultRef, source)
	}
	return strings.TrimSpace(out), ""
}

// isAncestorCommit reports whether ancestor is a local commit reachable from oid. A force push
// of rewritten history leaves the old remote head unreachable; diffing against it would count
// every commit the rebase picked up, so the caller falls back to the merge base instead.
func isAncestorCommit(ctx context.Context, repoDir, ancestor, oid string) bool {
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	_, err := util.RunGit(gitCtx, repoDir, "merge-base", "--is-ancestor", ancestor, oid)
	return err == nil
}
