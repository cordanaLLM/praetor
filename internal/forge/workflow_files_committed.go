// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	slashpath "path"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrCommittedWorkflowAbsent reports that a workflow document a commit records is not in the
// checkout's object store, as in a blobless partial clone (git clone --filter=blob:none) that
// never fetched it. Nothing is fetched to read it (util.RunGitProbe), so the caller decides what
// to read instead and says so.
var ErrCommittedWorkflowAbsent = errors.New("its object is not in this checkout, and nothing is fetched to read it")

const (
	// committedWorkflowRecordBytes bounds one record of the tree listing of a commit's workflow
	// directory: mode, type, object and a path of up to 255 bytes below .github/workflows, with
	// room to spare. The listing of maxWorkflowFiles records and one more fits the bound, so a
	// directory that outgrows the inventory is refused by its count, not cut.
	committedWorkflowRecordBytes = 512
	// committedObjectRecordBytes bounds one answer line of git cat-file --batch-check: an object
	// name of up to 64 hex digits, its type and its size, or the word missing.
	committedObjectRecordBytes = 128
	// committedObjectFormat is the --batch-check answer format checkCommittedObject parses.
	committedObjectFormat = "--batch-check=%(objectname) %(objecttype) %(objectsize)"
)

// readCommittedWorkflowFiles reads the workflow documents directly under .github/workflows of
// commit, a commit of the checkout at repoPath, in name order, as readWorkflowFiles reads them from
// the working tree: the directory relative to repoPath, at most maxWorkflowFiles entries in it, a
// YAML file read whole up to contextopt.MaxSourceBytes, and a YAML entry that is no regular file,
// such as a symlink, refused. A commit without the directory yields no files. Nothing is checked
// out and nothing is fetched (util.RunGitProbe): a document the checkout does not hold is
// ErrCommittedWorkflowAbsent (checkCommittedWorkflowObjects).
func readCommittedWorkflowFiles(ctx context.Context, repoPath, commit string) ([]workflowFile, error) {
	entries, err := listCommittedWorkflows(ctx, repoPath, commit)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	if err := checkCommittedWorkflowObjects(ctx, repoPath, commit, entries); err != nil {
		return nil, err
	}
	files := make([]workflowFile, 0, len(entries))
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		name := slashpath.Base(entries[i].Path)
		blob, err := util.RunGitProbe(ctx, repoPath, contextopt.MaxSourceBytes+1, "cat-file", "blob", entries[i].Object)
		if err != nil {
			return nil, fmt.Errorf("workflow %s at commit %s: %w", name, commit, err)
		}
		files = append(files, workflowFile{Name: name, Data: blob.Stdout})
	}
	return files, nil
}

// listCommittedWorkflows lists the workflow documents directly under .github/workflows of commit.
// An entry that is a directory or not a YAML file is no workflow and is left out, as
// isWorkflowDocument skips it on disk; a YAML entry that is no regular blob is refused. The
// listing reads trees only, so it answers in a partial clone that lacks the documents themselves.
func listCommittedWorkflows(ctx context.Context, repoPath, commit string) ([]util.GitTreeEntry, error) {
	if err := util.ValidateExecArg(commit); err != nil {
		return nil, fmt.Errorf("workflows at commit %q: %w", commit, err)
	}
	listing, err := util.RunGitProbe(ctx, repoPath, (maxWorkflowFiles+1)*committedWorkflowRecordBytes,
		"--literal-pathspecs", "ls-tree", "-z", commit, "--", "./"+ghworkflow.Dir+"/")
	if err != nil {
		return nil, fmt.Errorf("list the workflows of commit %s: %w", commit, err)
	}
	entries, err := util.ParseGitTreeListing(listing.Stdout, false, maxWorkflowFiles)
	if err != nil {
		return nil, fmt.Errorf("list the workflows of commit %s: %w", commit, err)
	}
	workflows := make([]util.GitTreeEntry, 0, len(entries))
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		name := slashpath.Base(entries[i].Path)
		if entries[i].Type == "tree" || !ghworkflow.IsYAMLName(name) {
			continue
		}
		if !entries[i].RegularBlob() {
			return nil, fmt.Errorf("workflow %s at commit %s is not a regular file (mode %s)", name, commit, entries[i].Mode)
		}
		workflows = append(workflows, entries[i])
	}
	return workflows, nil
}

// checkCommittedWorkflowObjects asks git once, through cat-file --batch-check, for the type and
// size of the object of every entry, without reading or fetching one. An object the checkout does
// not hold is ErrCommittedWorkflowAbsent naming its workflow, and one larger than
// contextopt.MaxSourceBytes is refused (checkCommittedObject).
func checkCommittedWorkflowObjects(ctx context.Context, repoPath, commit string, entries []util.GitTreeEntry) error {
	var request strings.Builder
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		request.WriteString(entries[i].Object + "\n")
	}
	stdinCtx, err := util.WithCommandStdin(ctx, []byte(request.String()))
	if err != nil {
		return fmt.Errorf("inspect the workflows of commit %s: %w", commit, err)
	}
	answer, err := util.RunGitProbe(stdinCtx, repoPath, (len(entries)+1)*committedObjectRecordBytes, "cat-file", committedObjectFormat)
	if err != nil {
		return fmt.Errorf("inspect the workflows of commit %s: %w", commit, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(answer.Stdout), "\n"), "\n")
	if len(lines) != len(entries) {
		return fmt.Errorf("inspect the workflows of commit %s: git answered %d lines for %d objects", commit, len(lines), len(entries))
	}
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		if err := checkCommittedObject(entries[i], lines[i], commit); err != nil {
			return err
		}
	}
	return nil
}

// checkCommittedObject checks one --batch-check answer for the object of entry: "<object> missing"
// is ErrCommittedWorkflowAbsent, and anything but "<object> blob <size>" with a size up to
// contextopt.MaxSourceBytes is refused.
func checkCommittedObject(entry util.GitTreeEntry, answer, commit string) error {
	name := slashpath.Base(entry.Path)
	fields := strings.Fields(answer)
	if len(fields) == 2 && fields[0] == entry.Object && fields[1] == "missing" {
		return fmt.Errorf("workflow %s at commit %s: %w", name, commit, ErrCommittedWorkflowAbsent)
	}
	if len(fields) != 3 || fields[0] != entry.Object || fields[1] != "blob" {
		return fmt.Errorf("workflow %s at commit %s: unexpected object answer %q", name, commit, answer)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("workflow %s at commit %s: unexpected object size in %q", name, commit, answer)
	}
	if size > contextopt.MaxSourceBytes {
		return fmt.Errorf("workflow %s at commit %s exceeds %d bytes", name, commit, contextopt.MaxSourceBytes)
	}
	return nil
}
