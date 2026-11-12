// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// predictMerge accepts Git's conflict status while requiring complete, parseable conflict evidence.
func predictMerge(ctx context.Context, repo string, b *Bundle) error {
	out, status, err := util.RunGitProbeStatus(ctx, repo, util.MaxCommandOutputBytes, "merge-tree", "--write-tree", "--name-only", "-z", "--messages", b.Target, b.Head)
	if err != nil {
		return fmt.Errorf("predict merge: %w", err)
	}
	if err := parseMerge(out.Stdout, b); err != nil {
		return err
	}
	if status == 1 && len(b.Conflicts) == 0 {
		return fmt.Errorf("merge-tree reported conflicts without conflict evidence")
	}
	return nil
}

// parseMerge reads Git 2.56's -z tree, conflicted-path list, and counted message records.
func parseMerge(data []byte, b *Bundle) error {
	fields := strings.Split(string(data), "\x00")
	if len(fields) < 3 || fields[len(fields)-1] != "" || !util.ValidGitObjectID(fields[0]) {
		return fmt.Errorf("invalid merge-tree output")
	}
	b.Tree = fields[0]
	i := 1
	for ; i < len(fields) && fields[i] != ""; i++ {
		b.ConflictedPaths = append(b.ConflictedPaths, fields[i])
	}
	if i == len(fields) {
		return fmt.Errorf("merge-tree conflict list missing terminator")
	}
	i++
	for records := 0; records < len(fields) && i < len(fields)-1; records++ {
		next, err := parseMergeMessage(fields, i, b)
		if err != nil {
			return err
		}
		i = next
	}
	completeConflictKinds(b)
	return nil
}

// parseMergeMessage consumes a path-count, paths, stable kind and human message without splitting paths.
func parseMergeMessage(fields []string, i int, b *Bundle) (int, error) {
	count, err := strconv.Atoi(fields[i])
	if err != nil || count < 1 || count > len(fields)-i-4 {
		return i, fmt.Errorf("invalid merge-tree message path count %q", fields[i])
	}
	kind := fields[i+count+1]
	if strings.HasPrefix(kind, "CONFLICT") {
		for p := 0; p < count; p++ {
			path := fields[i+p+1]
			if path == "" {
				return i, fmt.Errorf("merge-tree conflict has empty path")
			}
			b.Conflicts = append(b.Conflicts, Conflict{Path: path, Kind: kind})
			if !slices.Contains(b.ConflictedPaths, path) {
				b.ConflictedPaths = append(b.ConflictedPaths, path)
			}
		}
	}
	return i + count + 3, nil
}

// completeConflictKinds supplies an explicit unmerged kind when Git lists only an index conflict.
func completeConflictKinds(b *Bundle) {
	classified := make(map[string]bool, len(b.Conflicts))
	for i := 0; i < len(b.Conflicts); i++ {
		classified[b.Conflicts[i].Path] = true
	}
	for i := 0; i < len(b.ConflictedPaths); i++ {
		path := b.ConflictedPaths[i]
		if !classified[path] {
			b.Conflicts = append(b.Conflicts, Conflict{Path: path, Kind: "unmerged"})
		}
	}
}
