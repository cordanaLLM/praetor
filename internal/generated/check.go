// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Modes of a report.
const (
	// ModePullRequest is Check: the change may not edit a declared artefact, and every artefact
	// must render from its sources.
	ModePullRequest = "pull-request"
	// ModeRender is Render: every artefact is rendered and compared with the committed file.
	ModeRender = "render"
)

// Rendering outcomes of one artefact.
const (
	RenderedOK      = "ok"
	RenderedFailed  = "failed"
	RenderedSkipped = "skipped"
)

// maxNamedPaths bounds the paths one problem line names.
const maxNamedPaths = 5

// Report is the outcome of one Check or Render. Passed is false exactly when Problems lists
// something.
type Report struct {
	Mode      string `json:"mode"`
	Root      string `json:"root"`
	Base      string `json:"base,omitempty"`
	MergeBase string `json:"merge_base,omitempty"`
	Head      string `json:"head"`
	Branch    string `json:"branch,omitempty"`
	Title     string `json:"title,omitempty"`
	// Marker is the regeneration marker of the base, never of the change: a change cannot
	// declare the marker that admits it.
	Marker       Marker `json:"regeneration_marker"`
	Regeneration bool   `json:"regeneration"`
	ChangedPaths int    `json:"changed_paths"`
	// Outside lists what a regeneration change edits beyond the declared artefacts.
	Outside   []string `json:"outside,omitempty"`
	Artefacts []Result `json:"artefacts"`
	// Written lists the files Render wrote into the checkout.
	Written  []string `json:"written,omitempty"`
	Problems []string `json:"problems,omitempty"`
	Passed   bool     `json:"passed"`
}

// Result is what one check or rendering found for one artefact.
type Result struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Active bool   `json:"active"`
	Reason string `json:"reason,omitempty"`
	// Edited lists the artefact's paths the change edits (Check).
	Edited []string `json:"edited,omitempty"`
	// SourcesChanged lists the artefact's sources the change edits: the artefact needs the
	// regeneration change once the change lands (Check).
	SourcesChanged []string `json:"sources_changed,omitempty"`
	Rendered       string   `json:"rendered"`
	Failure        string   `json:"failure,omitempty"`
	// Changed lists the files whose rendering differs from the committed file.
	Changed []string `json:"changed,omitempty"`
}

// CheckOptions selects one pull-request check.
type CheckOptions struct {
	// Root is any directory of the checkout.
	Root string
	// Base is the branch or commit the change merges into; Head is its head commit, HEAD when
	// empty.
	Base, Head string
	// Branch and Title are the change's branch and title, which carry the regeneration marker.
	Branch, Title string
	// Runner runs the render commands; nil runs them for real.
	Runner Runner
}

// rendering is what running the render commands in a worktree produced.
type rendering struct {
	failures map[string]string
	changed  map[string][]string
	empty    map[string]bool
}

// Check judges the change merge-base(Base, Head)..Head as a pull request (ADR-0017). It fails
// when the change edits a declared artefact, unless the change carries the base's regeneration
// marker; a regeneration change in turn fails when it edits anything else or commits a file that
// differs from its rendering. Every artefact that applies at Head is rendered in a temporary
// worktree of Head, and an artefact whose command fails, or whose rendering selects no file,
// fails the check: the gate fails closed. The declared set is the union of the base's and the
// head's, so a change cannot drop a declaration and edit the artefact at once.
func Check(ctx context.Context, opts CheckOptions) (report *Report, err error) {
	repo, err := openRepository(ctx, opts.Root)
	if err != nil {
		return nil, err
	}
	head, err := repo.commit(ctx, defaultHead(opts.Head))
	if err != nil {
		return nil, err
	}
	mergeBase, err := repo.mergeBase(ctx, opts.Base, head)
	if err != nil {
		return nil, err
	}
	changed, err := repo.changedPaths(ctx, mergeBase, head)
	if err != nil {
		return nil, err
	}
	sess, err := openSession(ctx, repo.root, head)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, sess.close(ctx)) }()
	report = &Report{Mode: ModePullRequest, Root: repo.root, Base: opts.Base, MergeBase: mergeBase, Head: head,
		Branch: opts.Branch, Title: opts.Title, ChangedPaths: len(changed)}
	return report, judgeChange(ctx, report, opts, judged{changed: changed, base: CommitTree(repo.root, mergeBase),
		head: DirTree(sess.path), dir: sess.path})
}

// judged is the material one pull-request check reads.
type judged struct {
	changed []string
	base    Tree
	head    Tree
	dir     string
}

// judgeChange fills report from the change's edits and the rendering of its head.
func judgeChange(ctx context.Context, report *Report, opts CheckOptions, in judged) error {
	baseSet, err := resolveTree(ctx, in.base)
	if err != nil {
		return fmt.Errorf("the base's declaration: %w", err)
	}
	headSet, err := resolveTree(ctx, in.head)
	if err != nil {
		return fmt.Errorf("the change's declaration: %w", err)
	}
	report.Marker = baseSet.Marker
	report.Regeneration = baseSet.Marker.Matches(opts.Branch, opts.Title)
	guarded := guardedArtefacts(baseSet, headSet)
	edits, outside, err := classifyChanges(ctx, guarded, in.changed, in.base, in.head)
	if err != nil {
		return err
	}
	rendered, err := renderIn(ctx, opts.Runner, in.head, in.dir, headSet.Active())
	if err != nil {
		return err
	}
	report.Artefacts = checkResults(guarded, edits, in.changed, rendered)
	if report.Regeneration {
		report.Outside = outside
	}
	report.Problems = checkProblems(report)
	report.Passed = len(report.Problems) == 0
	return nil
}

// defaultHead is head, or HEAD when it is empty.
func defaultHead(head string) string {
	if strings.TrimSpace(head) == "" {
		return "HEAD"
	}
	return head
}

// guardedArtefacts is the union of the artefacts that apply at the base and at the head, in the
// head's order: an artefact the change stops declaring, declines or deactivates stays guarded.
func guardedArtefacts(base, head *Set) []Artefact {
	guarded := head.Active()
	seen := make(map[string]bool, len(guarded))
	for index := 0; index < len(guarded); index++ {
		seen[guarded[index].Name] = true
	}
	baseActive := base.Active()
	for index := 0; index < len(baseActive); index++ {
		if !seen[baseActive[index].Name] {
			guarded = append(guarded, baseActive[index])
		}
	}
	return guarded
}

// classifyChanges assigns every changed path to the artefacts it edits. A path a whole-file
// artefact selects edits it; a path only block artefacts select edits each block whose text
// differs between base and head, and is outside the artefacts when the rest of the file differs
// too. Every other path is outside.
func classifyChanges(ctx context.Context, guarded []Artefact, changed []string, base, head Tree) (map[string][]string, []string, error) {
	edits := make(map[string][]string)
	var outside []string
	for index := 0; index < len(changed) && index < MaxTreeFiles; index++ {
		rel := changed[index]
		whole, blocks := owners(guarded, rel)
		for position := 0; position < len(whole); position++ {
			edits[whole[position].Name] = append(edits[whole[position].Name], rel)
		}
		if len(whole) > 0 {
			continue
		}
		if len(blocks) == 0 {
			outside = append(outside, rel)
			continue
		}
		editedBlocks, beyond, err := compareBlocks(ctx, rel, blocks, base, head)
		if err != nil {
			return nil, nil, err
		}
		for position := 0; position < len(editedBlocks); position++ {
			edits[editedBlocks[position]] = append(edits[editedBlocks[position]], rel)
		}
		if beyond {
			outside = append(outside, rel)
		}
	}
	return edits, outside, nil
}

// owners splits the artefacts selecting rel into whole-file and block artefacts.
func owners(guarded []Artefact, rel string) (whole, blocks []Artefact) {
	for index := 0; index < len(guarded); index++ {
		switch {
		case !guarded[index].owns(rel):
		case guarded[index].Block == nil:
			whole = append(whole, guarded[index])
		default:
			blocks = append(blocks, guarded[index])
		}
	}
	return whole, blocks
}

// compareBlocks names the block artefacts whose block in rel differs between base and head, and
// reports whether the file differs outside every block as well. A block whose markers cannot be
// read on either side counts as edited, and so does the rest of the file: a damaged marker is an
// edit of the generated region, never a pass.
func compareBlocks(ctx context.Context, rel string, blocks []Artefact, base, head Tree) ([]string, bool, error) {
	before, _, err := readText(ctx, base, rel)
	if err != nil {
		return nil, false, err
	}
	after, _, err := readText(ctx, head, rel)
	if err != nil {
		return nil, false, err
	}
	var edited []string
	restBefore, restAfter := before, after
	damaged := false
	for index := 0; index < len(blocks); index++ {
		block := blocks[index].Block
		oldBlock, _, oldErr := extractBlock(before, block)
		newBlock, _, newErr := extractBlock(after, block)
		if oldErr != nil || newErr != nil || oldBlock != newBlock {
			edited = append(edited, blocks[index].Name)
		}
		restBefore, oldErr = withoutBlock(restBefore, block, oldErr)
		restAfter, newErr = withoutBlock(restAfter, block, newErr)
		damaged = damaged || oldErr != nil || newErr != nil
	}
	return edited, damaged || restBefore != restAfter, nil
}

// withoutBlock replaces content's block by one empty line, so two files compare by what lies
// outside it. A content without the block is returned unchanged; a block that cannot be read,
// or an earlier error carried in prior, is returned as an error.
func withoutBlock(content string, block *Block, prior error) (string, error) {
	if prior != nil {
		return content, prior
	}
	if _, found, err := extractBlock(content, block); err != nil || !found {
		return content, err
	}
	return replaceBlock(content, "", block)
}

// renderIn renders the artefacts in dir, whose tree is tree, and compares every artefact's state
// before and after.
func renderIn(ctx context.Context, run Runner, tree Tree, dir string, artefacts []Artefact) (rendering, error) {
	if run == nil {
		run = execCommand
	}
	before, err := snapshot(ctx, tree, artefacts)
	if err != nil {
		return rendering{}, err
	}
	failures, err := runJobs(ctx, run, dir, renderJobs(artefacts))
	if err != nil {
		return rendering{}, err
	}
	after, err := snapshot(ctx, tree, artefacts)
	if err != nil {
		return rendering{}, err
	}
	result := rendering{failures: failures, changed: map[string][]string{}, empty: map[string]bool{}}
	for index := 0; index < len(artefacts); index++ {
		name := artefacts[index].Name
		result.changed[name] = changedPaths(before[name], after[name])
		result.empty[name] = len(after[name]) == 0
	}
	return result, nil
}

// checkResults builds one result per guarded artefact.
func checkResults(guarded []Artefact, edits map[string][]string, changed []string, rendered rendering) []Result {
	results := make([]Result, 0, len(guarded))
	for index := 0; index < len(guarded); index++ {
		artefact := guarded[index]
		result := renderResult(artefact, rendered)
		result.Edited = edits[artefact.Name]
		for position := 0; position < len(changed) && position < MaxTreeFiles; position++ {
			if artefact.readsSource(changed[position]) && !artefact.owns(changed[position]) {
				result.SourcesChanged = append(result.SourcesChanged, changed[position])
			}
		}
		results = append(results, result)
	}
	return results
}

// renderResult is the rendering part of one artefact's result.
func renderResult(artefact Artefact, rendered rendering) Result {
	result := Result{Name: artefact.Name, Origin: artefact.Origin, Active: artefact.Active, Reason: artefact.Reason, Rendered: RenderedOK}
	_, ran := rendered.changed[artefact.Name]
	switch failure := rendered.failures[artefact.Name]; {
	case !ran:
		result.Rendered = RenderedSkipped
	case failure != "":
		result.Rendered, result.Failure = RenderedFailed, failure
	case rendered.empty[artefact.Name]:
		result.Rendered = RenderedFailed
		result.Failure = "the rendering selects no file of " + strings.Join(artefact.Paths, ", ")
	}
	result.Changed = rendered.changed[artefact.Name]
	return result
}

// checkProblems lists why a pull-request check fails.
func checkProblems(report *Report) []string {
	var problems []string
	for index := 0; index < len(report.Artefacts); index++ {
		result := report.Artefacts[index]
		if result.Rendered == RenderedFailed {
			problems = append(problems, fmt.Sprintf("%s: does not render: %s", result.Name, result.Failure))
		}
		switch {
		case len(result.Edited) > 0 && !report.Regeneration:
			problems = append(problems, fmt.Sprintf("%s: the change edits %s; only a regeneration change (%s) may edit a declared generated artefact",
				result.Name, namedPaths(result.Edited), report.Marker))
		case report.Regeneration && len(result.Changed) > 0:
			problems = append(problems, fmt.Sprintf("%s: the regeneration change commits %s, which differ from their rendering",
				result.Name, namedPaths(result.Changed)))
		}
	}
	if len(report.Outside) > 0 {
		problems = append(problems, fmt.Sprintf("a regeneration change edits only declared generated artefacts, but this one also edits %s",
			namedPaths(report.Outside)))
	}
	return problems
}

// namedPaths names the first paths and counts the rest.
func namedPaths(paths []string) string {
	if len(paths) <= maxNamedPaths {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:maxNamedPaths], ", "), len(paths)-maxNamedPaths)
}
