// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RenderOptions selects one rendering of the default branch.
type RenderOptions struct {
	// Root is any directory of the checkout; its HEAD commit is rendered.
	Root string
	// Check reports a difference without writing anything.
	Check bool
	// Runner runs the render commands; nil runs them for real.
	Runner Runner
}

// Render renders every artefact that applies at the checkout's HEAD in a temporary worktree of
// HEAD and reports which ones differ from the committed files (ADR-0017). With Check a
// difference is a problem, so a scheduled job or a release gate fails on a stale default
// branch; without it the changed files, or blocks, are copied into the checkout for the
// regeneration change to commit. A failed command, or a rendering that selects no file, is a
// problem in either mode, and nothing is written then. A file the checkout changed since HEAD is
// never overwritten: the rendering read HEAD, not those changes.
func Render(ctx context.Context, opts RenderOptions) (report *Report, err error) {
	repo, err := openRepository(ctx, opts.Root)
	if err != nil {
		return nil, err
	}
	head, err := repo.commit(ctx, "HEAD")
	if err != nil {
		return nil, err
	}
	sess, err := openSession(ctx, repo.root, head)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, sess.close(ctx)) }()
	tree := DirTree(sess.path)
	set, err := resolveTree(ctx, tree)
	if err != nil {
		return nil, err
	}
	active := set.Active()
	rendered, err := renderIn(ctx, opts.Runner, tree, sess.path, active)
	if err != nil {
		return nil, err
	}
	report = &Report{Mode: ModeRender, Root: repo.root, Head: head, Marker: set.Marker,
		Artefacts: checkResults(set.Artefacts, nil, nil, rendered)}
	report.Problems = renderProblems(report, opts.Check)
	if len(report.Problems) == 0 && !opts.Check {
		if report.Written, err = writeBack(ctx, repo.root, head, sess.path, active, rendered.changed); err != nil {
			return report, err
		}
	}
	report.Passed = len(report.Problems) == 0
	return report, nil
}

// renderProblems lists why a rendering fails: an artefact that does not render and, under
// check, one whose rendering differs from the committed file.
func renderProblems(report *Report, check bool) []string {
	var problems []string
	for index := 0; index < len(report.Artefacts); index++ {
		result := report.Artefacts[index]
		if result.Rendered == RenderedFailed {
			problems = append(problems, fmt.Sprintf("%s: does not render: %s", result.Name, result.Failure))
		}
		if check && len(result.Changed) > 0 {
			problems = append(problems, fmt.Sprintf("%s: stale: %s differ from their rendering; land a regeneration change",
				result.Name, NamedPaths(result.Changed)))
		}
	}
	return problems
}

// writeBack copies each changed artefact file, or block, from the render worktree into the
// checkout at root. It first proves every target unchanged since head, so it writes all of them
// or none.
func writeBack(ctx context.Context, root, head, renderDir string, artefacts []Artefact, changed map[string][]string) ([]string, error) {
	checkout, committed, rendered := DirTree(root), CommitTree(root, head), DirTree(renderDir)
	var targets []writeTarget
	for index := 0; index < len(artefacts); index++ {
		paths := changed[artefacts[index].Name]
		for position := 0; position < len(paths); position++ {
			targets = append(targets, writeTarget{rel: paths[position], block: artefacts[index].Block})
		}
	}
	for index := 0; index < len(targets); index++ {
		if err := requireUnchanged(ctx, checkout, committed, targets[index].rel); err != nil {
			return nil, err
		}
	}
	written := make([]string, 0, len(targets))
	for index := 0; index < len(targets); index++ {
		if err := targets[index].write(ctx, root, rendered); err != nil {
			return written, err
		}
		written = append(written, targets[index].rel)
	}
	sort.Strings(written)
	return written, nil
}

// requireUnchanged refuses a target whose checkout text differs from its text at head.
func requireUnchanged(ctx context.Context, checkout, committed Tree, rel string) error {
	current, held, err := readText(ctx, checkout, rel)
	if err != nil {
		return err
	}
	original, wasHeld, err := readText(ctx, committed, rel)
	if err != nil {
		return err
	}
	if held != wasHeld || current != original {
		return fmt.Errorf("%s has changes the commit does not hold; commit or discard them, then render again", rel)
	}
	return nil
}

// writeTarget is one file, or one block of a file, Render writes.
type writeTarget struct {
	rel   string
	block *Block
}

// write copies the target from the rendered tree into root: a removed file is removed, a block
// replaces the checkout's block and leaves the rest of the file as it is.
func (w writeTarget) write(ctx context.Context, root string, rendered Tree) error {
	data, exists, err := rendered.Read(ctx, w.rel)
	if err != nil {
		return err
	}
	if !exists {
		return removeFile(root, w.rel)
	}
	if w.block != nil {
		return w.writeBlock(ctx, root, string(data))
	}
	if err := util.MkdirConfined(root, filepath.FromSlash(path.Dir(w.rel)), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", w.rel, err)
	}
	return util.WriteFileConfined(root, filepath.FromSlash(w.rel), data, util.TrackedFilePerm)
}

// writeBlock splices the rendered block into the checkout's copy of the file.
func (w writeTarget) writeBlock(ctx context.Context, root, renderedText string) error {
	normalized, _ := util.NormalizeLineEndings(renderedText)
	block, found, err := extractBlock(normalized, w.block)
	if err != nil || !found {
		return fmt.Errorf("write %s: the rendering carries no readable %s block: %w", w.rel, w.block.Start, err)
	}
	current, _, err := readText(ctx, DirTree(root), w.rel)
	if err != nil {
		return err
	}
	replaced, err := replaceBlock(current, block, w.block)
	if err != nil {
		return fmt.Errorf("write %s: %w", w.rel, err)
	}
	return util.WriteFileConfined(root, filepath.FromSlash(w.rel), []byte(replaced), util.TrackedFilePerm)
}

// removeFile removes rel below root, which the rendering no longer produces.
func removeFile(root, rel string) error {
	target, err := util.ConfinePath(root, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	return nil
}
