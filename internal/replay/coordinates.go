// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
)

// mapFixHunksToCutoff translates repair-parent spans through intervening edits, omitting new lines.
func mapFixHunksToCutoff(ctx context.Context, repoDir, cutoffSHA, parentSHA string, hunks []FixHunk) ([]FixHunk, error) {
	raw, err := readUnifiedZeroDiff(ctx, repoDir, cutoffSHA, parentSHA)
	if err != nil {
		return nil, err
	}
	changes, err := parseDiffHunksBounded(raw)
	if err != nil {
		return nil, err
	}
	var mapped []FixHunk
	for i := 0; i < len(hunks) && i < MaxDiffLines; i++ {
		spans := mapCutoffSpan(hunks[i], changes)
		mapped, err = appendUniqueHunks(mapped, spans)
		if err != nil {
			return nil, err
		}
	}
	return mapped, nil
}

// mapCutoffSpan splits a repair span around lines introduced after the cutoff.
func mapCutoffSpan(h FixHunk, changes []parsedHunk) []FixHunk {
	if h.File == "" {
		return nil
	}
	var spans []FixHunk
	for line, step := h.StartLine, 0; line <= h.EndLine && step < MaxDiffLines; line, step = line+1, step+1 {
		mapped, ok := cutoffLine(h.File, line, changes)
		if !ok {
			continue
		}
		n := len(spans)
		if n > 0 && spans[n-1].EndLine+1 == mapped {
			spans[n-1].EndLine = mapped
			continue
		}
		spans = append(spans, FixHunk{File: h.File, StartLine: mapped, EndLine: mapped})
	}
	return spans
}

// cutoffLine maps one parent-tree line back to its unchanged cutoff-tree location.
func cutoffLine(file string, line int, changes []parsedHunk) (int, bool) {
	delta := 0
	for i := 0; i < len(changes) && i < MaxDiffLines; i++ {
		h := changes[i]
		if h.newFile != file {
			continue
		}
		if h.oldFile == "" {
			return 0, false
		}
		if h.newCount > 0 && line >= h.newStart && line < h.newStart+h.newCount {
			return 0, false
		}
		end := h.newStart + h.newCount
		if h.newCount == 0 {
			end = h.newStart + 1
		}
		if line >= end {
			delta += h.oldCount - h.newCount
		}
	}
	return line + delta, true
}
