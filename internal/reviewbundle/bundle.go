// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

// Package reviewbundle reads review content from a predicted merge into a pinned target tip.
package reviewbundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxMetadataEntries bounds each path/conflict inventory and the merge message count.
const MaxMetadataEntries = 4096

// Options bounds retained file content and file count; zero selects the documented defaults.
// MaxBundleBytes counts content bytes, excluding path names and JSON metadata.
type Options struct {
	MaxFileBytes   int `json:"max_file_bytes"`
	MaxFiles       int `json:"max_files"`
	MaxBundleBytes int `json:"max_bundle_bytes"`
}

// Bundle records resolved commit IDs, the predicted tree, and every omitted or shortened path.
// OmittedPaths independently accounts for changed paths excluded by the file-count limit.
// ConflictedPaths preserves merge-tree evidence independently of the classified Conflicts.
type Bundle struct {
	Target          string     `json:"target"`
	Head            string     `json:"head"`
	MergeBase       string     `json:"merge_base"`
	Tree            string     `json:"tree"`
	Files           []File     `json:"files"`
	Conflicts       []Conflict `json:"conflicts"`
	Cut             []string   `json:"cut"`
	Limits          Options    `json:"limits"`
	ConflictedPaths []string   `json:"conflicted_paths"`
	OmittedPaths    []string   `json:"omitted_paths"`
}

// File is predicted content for a merge-affected path; binary and deleted files carry no content.
type File struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	Truncated      bool   `json:"truncated"`
	TruncatedBytes int    `json:"truncated_bytes"`
	Deleted        bool   `json:"deleted"`
	Binary         bool   `json:"binary"`
}

// Conflict identifies one path and Git's machine-readable conflict classification.
type Conflict struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// normalizeOptions supplies defaults and refuses budgets beyond the bounded Git runner.
func normalizeOptions(opts Options) (Options, error) {
	if opts.MaxFileBytes == 0 {
		opts.MaxFileBytes = 64 << 10
	}
	if opts.MaxFiles == 0 {
		opts.MaxFiles = 200
	}
	if opts.MaxFiles > MaxMetadataEntries {
		return Options{}, fmt.Errorf("bundle file metadata exceeds %d entries", MaxMetadataEntries)
	}
	if opts.MaxBundleBytes == 0 {
		opts.MaxBundleBytes = 2 << 20
	}
	values := [...]int{opts.MaxFileBytes, opts.MaxFiles, opts.MaxBundleBytes}
	for i := 0; i < len(values); i++ {
		if values[i] < 1 || values[i] > util.MaxCommandOutputBytes {
			return Options{}, fmt.Errorf("bundle limits must be positive and at most %d", util.MaxCommandOutputBytes)
		}
	}
	return opts, nil
}

// Build pins both refs, predicts their merge, and reads paths changed from target to the merge tree.
// Git exit 1 represents conflict findings, not a failed build; truncated metadata is an error.
func Build(ctx context.Context, repo, target, head string, opts Options) (Bundle, error) {
	limits, err := normalizeOptions(opts)
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Limits: limits, Files: []File{}, Conflicts: []Conflict{}, Cut: []string{}, ConflictedPaths: []string{}, OmittedPaths: []string{}}
	if b.Target, err = resolveCommit(ctx, repo, target); err != nil {
		return Bundle{}, err
	}
	if b.Head, err = resolveCommit(ctx, repo, head); err != nil {
		return Bundle{}, err
	}
	base, err := util.RunGitProbe(ctx, repo, 1024, "merge-base", b.Target, b.Head)
	if err != nil {
		return Bundle{}, fmt.Errorf("merge base of %q and %q: %w", target, head, err)
	}
	b.MergeBase = strings.TrimSpace(string(base.Stdout))
	if err := predictMerge(ctx, repo, &b); err != nil {
		return Bundle{}, err
	}
	paths, err := util.RunGitProbe(ctx, repo, util.MaxCommandOutputBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", b.Target, b.Tree, "--")
	if err != nil {
		return Bundle{}, fmt.Errorf("merge changed paths: %w", err)
	}
	if err := appendFiles(ctx, repo, paths.Stdout, &b); err != nil {
		return Bundle{}, err
	}
	if err := Validate(b); err != nil {
		return Bundle{}, fmt.Errorf("built bundle: %w", err)
	}
	return b, nil
}

// resolveCommit uses the shared resolver and names an absent or invalid reference in its error.
func resolveCommit(ctx context.Context, repo, ref string) (string, error) {
	oid, err := util.ResolveGitCommit(ctx, repo, ref)
	if err != nil {
		return "", fmt.Errorf("bundle ref %q: %w", ref, err)
	}
	if oid == "" {
		return "", fmt.Errorf("bundle ref %q is not a known commit", ref)
	}
	return oid, nil
}
