// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds on one run (HISS-02).
const (
	maxDocuments     = 4096
	maxDocumentBytes = 2 << 20
	maxStatusLines   = 64
)

// Finding is one reference that does not resolve, or one malformed directive.
type Finding struct {
	Doc     string
	Line    int
	Message string
}

// String renders the finding as file:line: message.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.Doc, f.Line, f.Message)
}

// Options selects what Run checks.
type Options struct {
	// Root is the top of the repository's git work tree.
	Root string
	// Package is the repository-relative directory of the CLI's main package.
	Package string
	// Commands maps every top-level command name the binary dispatches, aliases included,
	// to the name of its handler function in Package. The caller reads it from the live
	// dispatch table, so the command list is the binary's own.
	Commands map[string]string
}

// Report is the outcome of one run. The counts print on a clean run too: a report that lists
// only failures cannot be told apart from one that read nothing.
type Report struct {
	Documents   int
	Skipped     []string
	Invocations int
	Paths       int
	Findings    []Finding
}

// pendingPath is an absent path waiting for the operator-owned and ignored lookups.
type pendingPath struct {
	doc  string
	line int
	ref  pathReference
}

// run carries one check across every document.
type run struct {
	opts    Options
	index   *repositoryIndex
	tree    *sourceTree
	model   *CLIModel
	report  *Report
	pending []pendingPath
}

// Run checks every in-scope document of the repository at opts.Root.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if len(opts.Commands) == 0 || opts.Package == "" {
		return nil, errors.New("docs references: the CLI package and its command table are required")
	}
	index, inventory, err := loadIndex(ctx, opts.Root)
	if err != nil {
		return nil, err
	}
	tree, err := newSourceTree(opts.Root, inventory)
	if err != nil {
		return nil, err
	}
	model, err := buildModel(ctx, tree, opts)
	if err != nil {
		return nil, err
	}
	state := &run{opts: opts, index: index, tree: tree, model: model, report: &Report{}}
	if err := state.documents(ctx, inventory); err != nil {
		return nil, err
	}
	if err := state.resolvePending(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(state.report.Findings, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Doc, b.Doc), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Message, b.Message))
	})
	return state.report, nil
}

// buildModel derives each command's vocabulary from its handler's code. Aliases share their
// handler's vocabulary.
func buildModel(ctx context.Context, tree *sourceTree, opts Options) (*CLIModel, error) {
	model := &CLIModel{commands: map[string]*Vocabulary{}}
	byHandler := map[string]*Vocabulary{}
	for _, name := range slices.Sorted(maps.Keys(opts.Commands)) {
		handler := opts.Commands[name]
		vocabulary, done := byHandler[handler]
		if !done {
			var err error
			vocabulary, err = tree.vocabulary(ctx, opts.Package, handler)
			if err != nil {
				return nil, fmt.Errorf("command %s: %w", name, err)
			}
			byHandler[handler] = vocabulary
		}
		model.commands[name] = vocabulary
	}
	return model, nil
}

// documents checks every in-scope document of the inventory.
func (r *run) documents(ctx context.Context, inventory []string) error {
	for _, rel := range inventory {
		if !InScope(rel) {
			continue
		}
		if r.report.Documents+len(r.report.Skipped) >= maxDocuments {
			return fmt.Errorf("more than %d documents are in scope", maxDocuments)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := util.ReadConfinedLimited(r.opts.Root, rel, maxDocumentBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue // A tracked deletion is absent from the tree being checked.
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		content := string(data)
		if reason := frozenReason(rel, content); reason != "" {
			r.report.Skipped = append(r.report.Skipped, rel+": "+reason)
			continue
		}
		r.report.Documents++
		r.document(ctx, rel, content)
	}
	return nil
}

// document checks one document's candidates.
func (r *run) document(ctx context.Context, rel, content string) {
	candidates, problems := Scan(rel, content)
	r.report.Findings = append(r.report.Findings, problems...)
	for _, candidate := range candidates {
		for _, call := range Invocations(candidate, r.opts.Package) {
			r.report.Invocations++
			for _, problem := range r.model.Check(call) {
				r.report.Findings = append(r.report.Findings, Finding{Doc: rel, Line: candidate.Line, Message: problem})
			}
		}
		for _, ref := range pathReferences(candidate, r.index.tops) {
			r.report.Paths++
			problem, absent := r.index.pathProblem(ctx, r.tree, ref)
			switch {
			case absent:
				r.pending = append(r.pending, pendingPath{doc: rel, line: candidate.Line, ref: ref})
			case problem != "":
				r.report.Findings = append(r.report.Findings, Finding{Doc: rel, Line: candidate.Line, Message: problem})
			}
		}
	}
}

// resolvePending reports every absent path that is not operator-owned, not ignored and not
// named by the engine's own source.
func (r *run) resolvePending(ctx context.Context) error {
	if len(r.pending) == 0 {
		return nil
	}
	absent := make([]string, 0, len(r.pending))
	for _, pending := range r.pending {
		absent = append(absent, pending.ref.path)
	}
	slices.Sort(absent)
	allowed, err := localOnly(ctx, r.opts.Root, slices.Compact(absent))
	if err != nil {
		return err
	}
	literals, err := r.tree.engineLiterals(ctx)
	if err != nil {
		return err
	}
	for _, pending := range r.pending {
		ref := pending.ref
		if allowed[ref.path] || literals[ref.path] || literals[ref.path+"/"] || literals[ref.text] {
			continue
		}
		r.report.Findings = append(r.report.Findings, Finding{Doc: pending.doc, Line: pending.line,
			Message: fmt.Sprintf("path %q is not in the repository, not operator-owned, not ignored and not named by the engine's Go source", ref.text)})
	}
	return nil
}

// InScope reports whether rel is a document the check reads: README.md and every Markdown
// file under docs/ except docs/project-records/, which records past events as they were
// written and is never updated to match later code.
func InScope(rel string) bool {
	if rel == "README.md" {
		return true
	}
	return strings.HasPrefix(rel, "docs/") && strings.HasSuffix(rel, ".md") && !strings.HasPrefix(rel, "docs/project-records/")
}

// decisionRecord matches a numbered decision record.
var decisionRecord = regexp.MustCompile(`^docs/adr/\d{4}-[^/]+\.md$`)

// frozenStatuses maps a decision record status to why its body is not checked.
var frozenStatuses = map[string]string{
	"accepted":   "Accepted decision record; its body is immutable (docs/adr/README.md)",
	"superseded": "Superseded decision record; only its Status line may change (docs/adr/README.md)",
	"deprecated": "Deprecated decision record; its body is immutable (docs/adr/README.md)",
	"proposed":   "Proposed decision record; it names surfaces that do not exist until it is implemented",
	"draft":      "Draft decision record; it names surfaces that do not exist until it is implemented",
}

// frozenReason returns why a decision record is skipped, or "" when rel is checked. A record
// with no readable status is checked: an unrecognised status must not switch the check off.
func frozenReason(rel, content string) string {
	if !decisionRecord.MatchString(rel) {
		return ""
	}
	return frozenStatuses[recordStatus(content)]
}

// recordStatus returns the lower-cased first word under a record's "## Status" heading.
func recordStatus(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	heading := -1
	for index := 0; index < len(lines) && index < maxStatusLines; index++ {
		line := strings.TrimSpace(lines[index])
		if heading >= 0 && line != "" {
			fields := strings.Fields(strings.Trim(line, "*_ "))
			if len(fields) == 0 {
				return ""
			}
			return strings.ToLower(strings.Trim(fields[0], "*_.,;:—-"))
		}
		if strings.EqualFold(line, "## Status") {
			heading = index
		}
	}
	return ""
}
