package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxSyncRecords bounds the records state sync reads from one Git listing, the tracked index or
// the untracked names (HISS-02). It guards the binding's loops; it is not a repository-size
// limit. The work is bounded by bytes and time, as every other binding input is: each listing
// arrives through a probe capped in bytes (syncIndexBytes for the index, contextopt.MaxTotalBytes
// for the rest) within util.GitProbeTimeout, and untracked content is read within
// contextopt.MaxTotalBytes. 1<<20 lies above every count the index cap admits: an index record
// is at least 54 bytes ("H 100644 <40-hex id> 0", a tab, a one-byte path and the NUL), so
// 16 MiB holds at most 310,689 of them, and a growing repository reaches the byte or time bound,
// which reports its own size, long before this count. An untracked name can take two bytes, so
// for that listing the count is reachable and caps the per-path inspection.
const maxSyncRecords = 1 << 20

// syncIndexBytes caps the tracked-index listing the binding reads. It is
// util.MaxCommandOutputBytes, the cap util.RefuseGitStatusFilters already applies when it lists
// the same tracked paths before every state inspection, so the binding is never the narrower
// limit on repository size. The index listing grows with the repository; the other listings
// grow only with uncommitted changes and keep contextopt.MaxTotalBytes.
const syncIndexBytes = util.MaxCommandOutputBytes

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
			return "", ledgerInputError(root, name, err)
		}
		parts = append(parts, name, fmt.Sprintf("%x", sha256.Sum256(content)))
	}
	for _, name := range []string{bugMetaName, questionMetaName} {
		sidecar, err := bindSidecar(ctx, root, name)
		if err != nil {
			return "", err
		}
		parts = append(parts, sidecar...)
	}
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

// bindSidecar binds one ledger metadata sidecar when it exists. Ledgers without
// one keep the binding they had before that sidecar was introduced.
func bindSidecar(ctx context.Context, root, name string) ([]string, error) {
	content, present, err := contextopt.ObserveSnapshot(ctx, filepath.Join(root, WorkingDirName, name))
	if err != nil {
		return nil, ledgerInputError(root, name, err)
	}
	if !present {
		return nil, nil
	}
	return []string{name, fmt.Sprintf("%x", sha256.Sum256(content))}, nil
}

// syncGitProbe is one Git observation the state binding hashes: the name its errors use, the
// byte cap on its output, whether the output is a NUL-terminated listing whose records an
// overflow can count, what an operator does when the output outgrows the cap, and its argv.
type syncGitProbe struct {
	label   string
	limit   int
	listing bool
	remedy  string
	args    []string
}

const (
	// syncShrinkChanges is the remedy for a probe whose output grows with uncommitted changes.
	syncShrinkChanges = "commit, stash, ignore or remove working-tree changes, then sync again"
	// syncReportIndex is the remedy for an index listing over its cap, which only growth reaches.
	syncReportIndex = "the cap holds the listing in memory, so report the repository's tracked-file count to the Praetor maintainers"
)

// stateGitProbes lists the Git observations the binding hashes, in binding order. Order and
// argv are part of the binding: changing either reports every existing ledger stale once.
func stateGitProbes(gitState string) []syncGitProbe {
	head := []string{"rev-parse", "--verify", "HEAD"}
	if gitState == "unborn" {
		head = []string{"symbolic-ref", "--quiet", "HEAD"}
	}
	return []syncGitProbe{
		{label: "HEAD", limit: contextopt.MaxTotalBytes, args: head},
		{label: "index listing", limit: syncIndexBytes, listing: true, remedy: syncReportIndex,
			args: []string{"ls-files", "-v", "--stage", "-z", "--", ".", ":(top,exclude).workingdir"}},
		{label: "status", limit: contextopt.MaxTotalBytes, listing: true, remedy: syncShrinkChanges,
			args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=all", "--", ".", ":(top,exclude).workingdir"}},
		{label: "staged diff", limit: contextopt.MaxTotalBytes, remedy: syncShrinkChanges,
			args: []string{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--ignore-submodules=all", "--cached", "--", ".", ":(top,exclude).workingdir"}},
		{label: "unstaged diff", limit: contextopt.MaxTotalBytes, remedy: syncShrinkChanges,
			args: []string{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--ignore-submodules=all", "--", ".", ":(top,exclude).workingdir"}},
		{label: "untracked listing", limit: contextopt.MaxTotalBytes, listing: true, remedy: syncShrinkChanges,
			args: []string{"ls-files", "--others", "--exclude-standard", "-z", "--", ".", ":(top,exclude).workingdir"}},
	}
}

func stateGitBinding(ctx context.Context, root, gitState string) ([]string, error) {
	probes := stateGitProbes(gitState)
	parts := make([]string, 0, len(probes))
	for _, probe := range probes {
		output, err := runSyncProbe(ctx, root, probe)
		if err != nil {
			return nil, err
		}
		parts = append(parts, output)
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

// runSyncProbe runs one binding probe within util.GitProbeTimeout. Output that outgrows the
// probe's cap fails with the cap, for a listing the records read before it, and the remedy. Any
// other failure carries git's own diagnostic, which names the path git could not read, such as a
// tracked file replaced by a named pipe: a tracked path is a state input and fails closed.
func runSyncProbe(ctx context.Context, root string, probe syncGitProbe) (string, error) {
	result, err := util.RunGitTreeProbe(ctx, root, probe.limit, util.GitProbeTimeout, probe.args...)
	if err == nil {
		return string(result.Stdout), nil
	}
	if len(result.Stdout) < probe.limit {
		return "", fmt.Errorf("bind state Git %s: %w", probe.label, err)
	}
	read := ""
	if probe.listing {
		read = fmt.Sprintf(" after %d records", bytes.Count(result.Stdout, []byte{0}))
	}
	return "", fmt.Errorf("state synchronization cannot bind the Git %s: it exceeds %d bytes%s; %s: %w",
		probe.label, probe.limit, read, probe.remedy, err)
}

// validateSyncIndex refuses an index the binding cannot vouch for: more records than
// maxSyncRecords, a malformed record, a hidden (assume-unchanged or skip-worktree) entry, or a
// submodule.
func validateSyncIndex(listing string) error {
	if count := listingRecords(listing); count > maxSyncRecords {
		return fmt.Errorf("state synchronization lists %d index entries, over its bound of %d records per Git listing; "+
			"the bound guards a loop, not the repository's size, so report the count to the Praetor maintainers", count, maxSyncRecords)
	}
	rows := strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00")
	for _, row := range rows {
		if row == "" {
			continue
		}
		if len(row) < 2 || row[1] != ' ' {
			return fmt.Errorf("state synchronization index record is malformed")
		}
		if util.GitHiddenIndexReason(row[0]) != "" {
			return fmt.Errorf("state synchronization refuses assume-unchanged or skip-worktree index entries")
		}
		if strings.HasPrefix(row[2:], "160000 ") {
			return fmt.Errorf("state synchronization cannot verify nested submodule worktrees")
		}
	}
	return nil
}

// listingRecords counts the records of one NUL-terminated (-z) Git listing; a final record
// left unterminated still counts.
func listingRecords(listing string) int {
	count := strings.Count(listing, "\x00")
	if listing != "" && !strings.HasSuffix(listing, "\x00") {
		count++
	}
	return count
}

// stateUntrackedBinding binds each untracked path git lists, in git's sorted order, so the
// records are deterministic. A regular file of at most contextopt.MaxSourceBytes is bound by the
// SHA-256 of its bytes while the content budget of contextopt.MaxTotalBytes lasts. Every other
// path -- a symlink, a named pipe, socket or device, an untracked nested repository (git lists
// it as a directory), a file over the per-file bound, or one past the budget -- is bound by its
// metadata: type, size and modification time. Such a path is rarely a state input, and one stray
// file must not stop the ledger from being written. A name that is not local, or a path that
// cannot be inspected, still fails and names the path. The ledgers and tracked files are state
// inputs and fail closed instead (ledgerInputError, runSyncProbe).
func stateUntrackedBinding(ctx context.Context, root, listing string) ([]string, error) {
	if listing == "" {
		return nil, nil
	}
	if count := listingRecords(listing); count > maxSyncRecords {
		return nil, fmt.Errorf("state synchronization lists %d untracked paths, over its bound of %d records per Git listing; "+
			"add generated paths to .gitignore or remove them, then sync again", count, maxSyncRecords)
	}
	names := strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00")
	parts, budget := make([]string, 0, len(names)), int64(contextopt.MaxTotalBytes)
	for _, name := range names {
		record, spent, err := untrackedRecord(ctx, root, name, budget)
		if err != nil {
			return nil, err
		}
		parts, budget = append(parts, record), budget-spent
	}
	return parts, nil
}

// untrackedRecord binds one untracked path and returns its record and the content bytes it
// spent from budget. A content record is the 64-hex SHA-256 of the bytes; a metadata record
// starts with "metadata", so the two can never be mistaken for each other.
func untrackedRecord(ctx context.Context, root, name string, budget int64) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	if !filepath.IsLocal(name) || name == "." {
		return "", 0, fmt.Errorf("state synchronization untracked path %q is not local", name)
	}
	info, err := lstatUntracked(ctx, root, name)
	if err != nil {
		return "", 0, fmt.Errorf("bind untracked path %s: %w", name, err)
	}
	if contentRefusal(info, min(contextopt.MaxSourceBytes, budget)) != "" {
		return fmt.Sprintf("metadata %s %d %d", info.Mode().Type(), info.Size(), info.ModTime().UnixNano()), 0, nil
	}
	digest, size, err := contextopt.DigestBinarySnapshot(ctx, filepath.Join(root, name), contextopt.MaxSourceBytes)
	if err != nil {
		return "", 0, fmt.Errorf("bind untracked path %s: %w", name, err)
	}
	return digest, size, nil
}

// lstatUntracked inspects one untracked path without following a symlink at any component
// below root, through the confinement contextopt reads with. git lists an untracked nested
// repository with a trailing slash; cleaning the name inspects the directory itself.
func lstatUntracked(ctx context.Context, root, name string) (_ fs.FileInfo, err error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	dir, err := contextopt.OpenDirectoryIn(ctx, root, filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.Lstat(filepath.Base(clean))
}

// ledgerInputError names a ledger input that failed to bind by its repository-relative path,
// with the reason when it is not a regular file or is over the per-file bound. Ledger inputs
// fail closed: the binding covers their bytes, and metadata in their place would let an edit
// that keeps size and modification time pass as synchronized.
func ledgerInputError(root, name string, err error) error {
	rel := filepath.ToSlash(filepath.Join(WorkingDirName, name))
	if info, statErr := os.Lstat(filepath.Join(root, WorkingDirName, name)); statErr == nil {
		if reason := contentRefusal(info, contextopt.MaxSourceBytes); reason != "" {
			return fmt.Errorf("bind state ledger %s: %s, and ledger inputs fail closed: %w", rel, reason, err)
		}
	}
	return fmt.Errorf("bind state ledger %s: %w", rel, err)
}

// contentRefusal says why a path cannot be bound by its bytes, or "" when it can: a regular
// file of at most limit bytes.
func contentRefusal(info fs.FileInfo, limit int64) string {
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("not a regular file (mode %s)", info.Mode().Type())
	}
	if info.Size() > limit {
		return fmt.Sprintf("%d bytes, over the %d-byte limit", info.Size(), limit)
	}
	return ""
}

func stateLogHash(binding string, content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(binding+"\x00"), content...)))
}

// stateGitString runs one state observation through util.RunGitTreeProbe, the line-ending
// model every working-tree comparison shares: git's live index can carry stat entries an
// ordinary command refreshed under core.autocrlf=true, and reading that cache with the implicit
// false default made unchanged CRLF files alternate between dirty and clean on Windows.
func stateGitString(ctx context.Context, root string, args ...string) (string, error) {
	result, err := util.RunGitTreeProbe(ctx, root, contextopt.MaxTotalBytes, util.GitProbeTimeout, args...)
	return strings.TrimSpace(string(result.Stdout)), err
}

// rejectStateGitFilters refuses a repository in which a tracked path selects a clean or process
// filter that its own configuration defines, through the one probe util.GitWorkingTreeChanges
// uses: state observations run status and diff, which would execute it.
func rejectStateGitFilters(ctx context.Context, root string) error {
	err := util.RefuseGitStatusFilters(ctx, root)
	if errors.Is(err, util.ErrGitStatusFilters) {
		return fmt.Errorf("state inspection refuses configured Git clean/process filters: %w", err)
	}
	if err != nil {
		return fmt.Errorf("inspect state Git filters: %w", err)
	}
	return nil
}

// unbornHead is what stateGitHead reports for a branch that has no commit yet.
const unbornHead = "(unborn)"

// RecordedCommit returns the commit a record made at root names: the HEAD SHA, or "" when
// root is outside a Git worktree or its branch has no commit yet, since neither has a commit
// to name. A Git read that fails for any other reason (broken metadata, a timeout) is an
// error, never an empty commit. `state task archive`, `baseline --record` and adoption's
// baseline all stamp their records through it.
func RecordedCommit(ctx context.Context, root string) (string, error) {
	present, err := util.GitWorktreePresent(ctx, root)
	if err != nil {
		return "", fmt.Errorf("detect Git worktree: %w", err)
	}
	if !present {
		return "", nil
	}
	head, err := stateGitHead(ctx, root)
	if err != nil {
		return "", fmt.Errorf("read Git HEAD: %w", err)
	}
	if head == unbornHead {
		return "", nil
	}
	return head, nil
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
		return unbornHead, nil
	}
	return "", errors.Join(fmt.Errorf("missing HEAD has an invalid branch reference"), err)
}

// maxSyncPaths keeps the previous name compiling for its test until that test is rewritten.
const maxSyncPaths = maxSyncRecords
