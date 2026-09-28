// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"

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
	// PreviewKeep: the file differs and adoption would keep it, because --force was not passed,
	// audit does not compare the file (scaffold.auditLocked; the ruleset under a policy that
	// requires neither linear history nor signed commits), or the file could not be read to
	// compare. FilePreview.Diff holds what regenerating the file would change when it could be
	// read.
	PreviewKeep PreviewAction = "keep"
)

// FilePreview is what a dry run shows for one file adoption renders from repository state, so
// an operator reads the file before a real run writes it. Only the branch protection ruleset is
// previewed: its content follows from the effective policy and the workflows the run leaves, not
// from a fixed template.
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

// Text is the preview as the CLI and the MCP adopt tool both print it: a heading naming the path
// and the action, the note, then the rendered file for a create or the diff for an update or a
// keep. An unchanged file prints the heading alone.
func (p FilePreview) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n--- Preview: %s (%s) ---\n", p.Path, p.Action)
	if p.Note != "" {
		fmt.Fprintf(&b, "  %s\n", p.Note)
	}
	body := p.Content
	if body == "" {
		body = p.Diff
	}
	if body != "" {
		b.WriteString(strings.TrimSuffix(body, "\n") + "\n")
	}
	return b.String()
}

// scaffoldPreviewed is scaffoldFile for a file a dry run previews. A real run scaffolds sc. A
// dry run, which writes nothing, also records in AdoptReport.Previews what sc.rel would come to.
// The action is read off the state scaffoldFile returns for sc, so for the same sc the preview
// and the run decide alike. The caller renders sc.content from the tree the run leaves at that
// step (previewWorkflows for the ruleset), not from the disk a dry run left unwritten.
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
	previewed := sc
	if written := s.dryRunWrites[sc.rel]; state == scaffoldWritten && written != nil {
		// The bytes the run writes: a refresh keeps the file's own line endings (replacePriorText).
		previewed.content = written
	}
	preview := filePreview(previewed, state, before, exists && readErr == nil, exists)
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

// planDryRunWrite records, in a dry run, that rel would hold content once the run it previews
// has written it; a real run records nothing. The scaffold writes (scaffoldFile), the earlier
// text refresh (replacePriorText) and the --force replace (replaceExisting) call it, which
// covers every workflow adoption writes itself.
func (s *adoptSession) planDryRunWrite(rel string, content []byte) {
	if !s.opts.DryRun {
		return
	}
	if s.dryRunWrites == nil {
		s.dryRunWrites = make(map[string][]byte)
	}
	s.dryRunWrites[rel] = append([]byte{}, content...)
}

// planDryRunRemoval records, in a dry run, that the run it previews would remove rel, as the
// documentation gate removes its canonical files once the facet is disabled.
func (s *adoptSession) planDryRunRemoval(rel string) {
	if !s.opts.DryRun {
		return
	}
	if s.dryRunWrites == nil {
		s.dryRunWrites = make(map[string][]byte)
	}
	s.dryRunWrites[rel] = nil
}

// previewWorkflows is, in a dry run, what the run it previews writes before its branch-ruleset
// step, for forge.RenderRulesetForRepository to read over the disk: the writes and removals the
// dry run recorded (planDryRunWrite, planDryRunRemoval), the documentation gate's among them,
// and the detected flavor's workflows (plannedFlavorWorkflows), which a dry run does not apply,
// at every path no step recorded. A real run has written all of them by then and gets nil.
func (s *adoptSession) previewWorkflows(ctx context.Context) (map[string][]byte, error) {
	if !s.opts.DryRun {
		return nil, nil
	}
	flavorWorkflows, err := s.plannedFlavorWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	planned := maps.Clone(s.dryRunWrites)
	if planned == nil {
		planned = make(map[string][]byte, len(flavorWorkflows))
	}
	for i := 0; i < len(flavorWorkflows) && i < maxScaffoldedWorkflows; i++ {
		if _, recorded := planned[flavorWorkflows[i].Path]; !recorded {
			planned[flavorWorkflows[i].Path] = []byte(flavorWorkflows[i].Content)
		}
	}
	return planned, nil
}
