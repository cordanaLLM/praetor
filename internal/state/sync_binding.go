package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const maxSyncPaths = 10000

var syncMarker = regexp.MustCompile(`\n<!-- praetor-state:v1 sha256:([a-f0-9]{64}) -->\n$`)

// VerifyStateSync checks the latest STATE entry against current local inputs.
// It never initializes, repairs or writes a ledger. Historical entries without
// a binding must be superseded by an explicit successful SyncState call.
func VerifyStateSync(ctx context.Context, rootPath string) error {
	content, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootPath, WorkingDirName, "STATE.md"))
	if err != nil {
		return fmt.Errorf("read state synchronization: %w", err)
	}
	return verifyStateContent(ctx, rootPath, content)
}

// verifyStateContent checks the last sync marker of one STATE.md snapshot, so a
// caller that rewrites the file verifies exactly the bytes it read.
func verifyStateContent(ctx context.Context, rootPath string, content []byte) error {
	match := syncMarker.FindSubmatchIndex(content)
	if match == nil {
		return fmt.Errorf("state synchronization missing; run `praetorctl state sync .`")
	}
	snap, err := InspectState(ctx, rootPath)
	if err != nil {
		return err
	}
	binding, err := stateBinding(ctx, rootPath, snap)
	if err != nil {
		return err
	}
	if string(content[match[2]:match[3]]) != stateLogHash(binding, content[:match[0]]) {
		return fmt.Errorf("state synchronization stale; run praetorctl state sync . after staging and ledger updates")
	}
	return nil
}

// stateBinding derives the value the ledger entry is bound to. The repository path is one of
// its inputs, canonicalised rather than merely made absolute.
//
// Two callers reaching one repository must derive one binding, and the path they hold is not
// spelled the same way. On Windows the harness runs praetorctl from a Python temporary
// directory, whose name is the 8.3 short form C:\Users\RUNNER~1\AppData\Local\Temp that Go
// reports verbatim, while the hook first moves to the top level git reports, and git resolves
// its working directory through GetFinalPathNameByHandleW, which always answers with the long
// C:\Users\runneradmin\... Under filepath.Abs alone those are two bindings for one repository,
// so the verification that follows every commit and push refused them all as stale (#135).
//
// util.ResolveExistingPath is the one helper for this (HISS-19), and the Go counterpart of
// common.resolved_relative_to on the hook side. filepath.EvalSymlinks re-reads every component
// through FindFirstFile on Windows, which is what turns the short name into the long one; on
// POSIX it collapses an aliased ancestor such as macOS's /var to /private/var.
//
// The praetor-state:v1 marker is deliberately unchanged: existing ledgers report stale once
// and are reconciled by one sync. Bumping it would make them report missing instead, which is
// a worse message for the same situation.
func stateBinding(ctx context.Context, rootPath string, snap *StateSnapshot) (string, error) {
	root, err := util.ResolveExistingPath(ctx, rootPath)
	if err != nil {
		return "", err
	}
	parts := []string{"praetor-state:v1", root, snap.GitState, snap.Branch, snap.HeadSHA}
	for _, name := range []string{"OPEN.md", "BACKLOG.md", "BUGS.md", "QUESTIONS.md"} {
		content, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, WorkingDirName, name))
		if err != nil {
			return "", fmt.Errorf("bind state ledger %s: %w", name, err)
		}
		parts = append(parts, name, fmt.Sprintf("%x", sha256.Sum256(content)))
	}
	sidecar, err := bindBugSidecar(ctx, root)
	if err != nil {
		return "", err
	}
	parts = append(parts, sidecar...)
	if snap.GitState == "available" || snap.GitState == "unborn" {
		gitParts, err := stateGitBinding(ctx, root, snap.GitState)
		if err != nil {
			return "", err
		}
		parts = append(parts, gitParts...)
	}
	data, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// bindBugSidecar binds the bug metadata sidecar when it exists. Ledgers without
// one keep the binding they had before the sidecar was introduced.
func bindBugSidecar(ctx context.Context, root string) ([]string, error) {
	content, present, err := contextopt.ObserveSnapshot(ctx, filepath.Join(root, WorkingDirName, bugMetaName))
	if err != nil {
		return nil, fmt.Errorf("bind state ledger %s: %w", bugMetaName, err)
	}
	if !present {
		return nil, nil
	}
	return []string{bugMetaName, fmt.Sprintf("%x", sha256.Sum256(content))}, nil
}

func stateGitBinding(ctx context.Context, root, gitState string) ([]string, error) {
	commands := [][]string{
		{"rev-parse", "--verify", "HEAD"},
		{"ls-files", "-v", "--stage", "-z", "--", ".", ":(top,exclude).workingdir"},
		{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=all", "--", ".", ":(top,exclude).workingdir"},
		{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--ignore-submodules=all", "--cached", "--", ".", ":(top,exclude).workingdir"},
		{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--ignore-submodules=all", "--", ".", ":(top,exclude).workingdir"},
		{"ls-files", "--others", "--exclude-standard", "-z", "--", ".", ":(top,exclude).workingdir"},
	}
	if gitState == "unborn" {
		commands[0] = []string{"symbolic-ref", "--quiet", "HEAD"}
	}
	parts := make([]string, 0, len(commands))
	for _, args := range commands {
		result, err := util.RunGitProbe(ctx, root, contextopt.MaxTotalBytes, args...)
		if err != nil {
			return nil, fmt.Errorf("bind state Git %s: %w", args[0], err)
		}
		parts = append(parts, string(result.Stdout))
	}
	if err := validateSyncIndex(parts[1]); err != nil {
		return nil, err
	}
	untracked, err := stateUntrackedBinding(ctx, root, parts[len(parts)-1])
	if err != nil {
		return nil, err
	}
	return append(parts, untracked...), nil
}

func validateSyncIndex(listing string) error {
	rows := strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00")
	if len(rows) > maxSyncPaths {
		return fmt.Errorf("state synchronization exceeds %d index entries", maxSyncPaths)
	}
	for _, row := range rows {
		if row == "" {
			continue
		}
		if len(row) < 2 || row[1] != ' ' {
			return fmt.Errorf("state synchronization index record is malformed")
		}
		if row[0] == 'S' || row[0] >= 'a' && row[0] <= 'z' {
			return fmt.Errorf("state synchronization refuses assume-unchanged or skip-worktree index entries")
		}
		if strings.HasPrefix(row[2:], "160000 ") {
			return fmt.Errorf("state synchronization cannot verify nested submodule worktrees")
		}
	}
	return nil
}

func stateUntrackedBinding(ctx context.Context, root, listing string) ([]string, error) {
	if listing == "" {
		return nil, nil
	}
	names := strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00")
	if len(names) > maxSyncPaths {
		return nil, fmt.Errorf("state synchronization exceeds %d untracked files", maxSyncPaths)
	}
	parts, total := make([]string, 0, len(names)), 0
	for _, name := range names {
		if !filepath.IsLocal(name) || name == "." {
			return nil, fmt.Errorf("state synchronization untracked path is not local")
		}
		content, err := contextopt.ReadBinarySnapshot(ctx, filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("bind untracked state input: %w", err)
		}
		total += len(content)
		if total > contextopt.MaxTotalBytes {
			return nil, fmt.Errorf("state synchronization exceeds %d untracked bytes", contextopt.MaxTotalBytes)
		}
		parts = append(parts, fmt.Sprintf("%x", sha256.Sum256(content)))
	}
	return parts, nil
}

func stateLogHash(binding string, content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(binding+"\x00"), content...)))
}

func stateGitString(ctx context.Context, root string, args ...string) (string, error) {
	result, err := util.RunGitProbe(ctx, root, contextopt.MaxTotalBytes, args...)
	return strings.TrimSpace(string(result.Stdout)), err
}

func rejectStateGitFilters(ctx context.Context, root string) error {
	result, err := util.RunGitProbe(ctx, root, contextopt.MaxSourceBytes, "config", "--get-regexp", `^filter\..*\.(clean|process)$`)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(result.Stdout) == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect state Git filters: %w", err)
	}
	return fmt.Errorf("state inspection refuses configured Git clean/process filters")
}

func stateGitHead(ctx context.Context, root string) (string, error) {
	head, err := stateGitString(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD")
	if err == nil {
		return head, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return "", err
	}
	ref, err := stateGitString(ctx, root, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || !strings.HasPrefix(ref, "refs/heads/") {
		return "", errors.Join(fmt.Errorf("missing HEAD is not an unborn branch"), err)
	}
	_, err = stateGitString(ctx, root, "show-ref", "--verify", "--quiet", ref)
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "(unborn)", nil
	}
	return "", errors.Join(fmt.Errorf("missing HEAD has an invalid branch reference"), err)
}
