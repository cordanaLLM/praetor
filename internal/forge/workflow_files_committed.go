// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	slashpath "path"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// committedWorkflowRecordBytes bounds one record of the tree listing of a commit's workflow
// directory: mode, type, object, size and a path of up to 255 bytes below .github/workflows, with
// room to spare. The listing of maxWorkflowFiles records and one more fits the bound, so a
// directory that outgrows the inventory is refused by its count, not cut.
const committedWorkflowRecordBytes = 512

// readCommittedWorkflowFiles reads the workflow documents directly under .github/workflows of
// commit, a commit of the checkout at repoPath, in name order, as readWorkflowFiles reads them from
// the working tree: the directory relative to repoPath, at most maxWorkflowFiles entries in it, a
// YAML file read whole up to contextopt.MaxSourceBytes, and a YAML entry that is no regular file,
// such as a symlink, refused. A commit without the directory yields no files. Nothing is checked
// out and nothing is fetched (util.RunGitProbe).
func readCommittedWorkflowFiles(ctx context.Context, repoPath, commit string) ([]workflowFile, error) {
	if err := util.ValidateExecArg(commit); err != nil {
		return nil, fmt.Errorf("workflows at commit %q: %w", commit, err)
	}
	listing, err := util.RunGitProbe(ctx, repoPath, (maxWorkflowFiles+1)*committedWorkflowRecordBytes,
		"--literal-pathspecs", "ls-tree", "-z", "-l", commit, "--", "./"+ghworkflow.Dir+"/")
	if err != nil {
		return nil, fmt.Errorf("list the workflows of commit %s: %w", commit, err)
	}
	entries, err := util.ParseGitTreeListing(listing.Stdout, true, maxWorkflowFiles)
	if err != nil {
		return nil, fmt.Errorf("list the workflows of commit %s: %w", commit, err)
	}
	files := make([]workflowFile, 0, len(entries))
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		file, ok, err := readCommittedWorkflow(ctx, repoPath, commit, entries[i])
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, file)
		}
	}
	return files, nil
}

// readCommittedWorkflow reads the workflow document one listing entry of commit names. An entry
// that is a directory or not a YAML file is no workflow and is skipped (ok false), as
// isWorkflowDocument skips it on disk; a YAML entry that is no regular blob, or one larger than
// contextopt.MaxSourceBytes, is refused.
func readCommittedWorkflow(ctx context.Context, repoPath, commit string, entry util.GitTreeEntry) (workflowFile, bool, error) {
	name := slashpath.Base(entry.Path)
	if entry.Type == "tree" || !ghworkflow.IsYAMLName(name) {
		return workflowFile{}, false, nil
	}
	if !entry.RegularBlob() {
		return workflowFile{}, false, fmt.Errorf("workflow %s at commit %s is not a regular file (mode %s)", name, commit, entry.Mode)
	}
	if entry.Size > contextopt.MaxSourceBytes {
		return workflowFile{}, false, fmt.Errorf("workflow %s at commit %s exceeds %d bytes", name, commit, contextopt.MaxSourceBytes)
	}
	blob, err := util.RunGitProbe(ctx, repoPath, contextopt.MaxSourceBytes+1, "cat-file", "blob", entry.Object)
	if err != nil {
		return workflowFile{}, false, fmt.Errorf("workflow %s at commit %s: %w", name, commit, err)
	}
	return workflowFile{Name: name, Data: blob.Stdout}, true, nil
}
