// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// PreviewAction is what a dry run found adoption would do with one previewed file.
type PreviewAction string

const (
	// PreviewCreate: the file is absent and adoption would write it; FilePreview.Content holds it.
	PreviewCreate PreviewAction = "create"
	// PreviewUpdate: the file differs and adoption would replace it (--force); FilePreview.Diff
	// holds the change.
	PreviewUpdate PreviewAction = "update"
	// PreviewUnchanged: the file already holds what adoption renders, line endings aside.
	PreviewUnchanged PreviewAction = "unchanged"
	// PreviewKeep: the file differs and adoption would keep it, because --force was not passed
	// or the file could not be read to compare. FilePreview.Diff holds what --force would change
	// when the file could be read.
	PreviewKeep PreviewAction = "keep"
)

// FilePreview is what a dry run shows for one file adoption renders from repository state, so
// an operator reads the file before a real run writes it. Only the branch protection ruleset is
// previewed: its content follows from the effective policy and the workflows present, not from a
// fixed template.
type FilePreview struct {
	Path   string        `json:"path"`
	Action PreviewAction `json:"action"`
	// Content is the rendered file, for a create.
	Content string `json:"content,omitempty"`
	// Diff is the unified diff from the file on disk to the rendered one (util.UnifiedDiff),
	// for an update and for a keep whose file could be read.
	Diff string `json:"diff,omitempty"`
	// Note says what the rendering was derived from, or why the file could not be compared.
	Note string `json:"note,omitempty"`
}

// scaffoldPreviewed is scaffoldFile for a file a dry run previews. A real run scaffolds sc. A
// dry run, which writes nothing, also records in AdoptReport.Previews what sc.rel would come to.
// The action is read off the state scaffoldFile returns, so the preview and the run it previews
// cannot decide differently.
func (s *adoptSession) scaffoldPreviewed(ctx context.Context, sc scaffold, note string) error {
	if !s.opts.DryRun {
		_, err := s.scaffoldFile(ctx, sc)
		return err
	}
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return err
	}
	exists := fileExists(full)
	var before []byte
	var readErr error
	if exists {
		before, _, readErr = contextopt.ObserveSnapshot(ctx, full)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	state, err := s.scaffoldFile(ctx, sc)
	if err != nil {
		return err
	}
	preview := filePreview(sc, state, before, exists && readErr == nil, exists)
	preview.Note = note
	if readErr != nil {
		preview.Note = fmt.Sprintf("existing file could not be read to compare (%v); %s", readErr, note)
	}
	s.report.Previews = append(s.report.Previews, preview)
	return nil
}

// filePreview classifies what scaffoldFile decided (state) for a file that existed or not, and
// attaches the rendered content or the diff from before, the bytes read when readable.
func filePreview(sc scaffold, state scaffoldState, before []byte, readable, exists bool) FilePreview {
	preview := FilePreview{Path: sc.rel}
	switch {
	case !exists:
		preview.Action, preview.Content = PreviewCreate, string(sc.content)
		return preview
	case readable && (state == scaffoldIdentical || bytes.Equal(before, sc.content)):
		preview.Action = PreviewUnchanged
		return preview
	case state == scaffoldWritten:
		preview.Action = PreviewUpdate
	default:
		preview.Action = PreviewKeep
	}
	if readable {
		preview.Diff = util.UnifiedDiff(sc.rel, before, sc.content)
	}
	return preview
}
