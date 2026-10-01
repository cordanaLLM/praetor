// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package generated declares, lists, checks and renders a repository's generated artefacts
// (ADR-0017, #696): files the repository commits although other files determine them, such as
// the compiled context projections or the debt baseline.
//
// A pull request must not edit a declared artefact; it only proves that every artefact still
// renders from its sources (Check). Freshness, the rendering equal to the committed file, is a
// property of the default branch (Render), and one regeneration change per batch, recognised by
// a declared marker, is the only change allowed to touch the artefacts. Every rendering runs in a
// temporary worktree of the commit it judges, so neither mode writes outside it until Render
// copies a changed artefact back.
package generated

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Origins of a resolved artefact.
const (
	// OriginBuiltin marks one of Praetor's own artefacts (builtinArtefacts).
	OriginBuiltin = "builtin"
	// OriginManifest marks an artefact the manifest's generated.artefacts declares.
	OriginManifest = "manifest"
)

// Bounds of one resolution (HISS-02).
const (
	// MaxTreeFiles bounds the files one tree listing holds.
	MaxTreeFiles = 200000
	// MaxArtefactFiles bounds the files one artefact's paths may select.
	MaxArtefactFiles = 4096
	// MaxArtefactBytes bounds one artefact file that is read or compared.
	MaxArtefactBytes = 8 << 20
	// maxPathSegments bounds the segments of a path a glob is matched against.
	maxPathSegments = 256
)

// Block names the two whole lines that open and close a generated region of a file.
type Block struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Marker recognises a regeneration change: its branch starts with BranchPrefix and its title
// carries the conventional type TitleType (Matches).
type Marker struct {
	BranchPrefix string `json:"branch_prefix"`
	TitleType    string `json:"title_type"`
}

// Artefact is one declared artefact as it resolves in one tree.
type Artefact struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// Active says whether the artefact applies to the tree: a built-in one applies only where
	// its precondition holds (Reason says which failed); a declared one always applies.
	Active  bool     `json:"active"`
	Reason  string   `json:"reason,omitempty"`
	Paths   []string `json:"paths"`
	Block   *Block   `json:"block,omitempty"`
	Command []string `json:"command"`
	Env     []string `json:"env,omitempty"`
	Sources []string `json:"sources"`
	Timeout string   `json:"timeout"`
	// Files lists the tree's files the paths select; for a block artefact, only those carrying
	// the block.
	Files []string `json:"files"`

	timeout time.Duration
	paths   globSet
	sources globSet
}

// Set is the resolved declaration of one tree: the marker, the declined built-in artefacts and
// every artefact in render order, built-in ones first.
type Set struct {
	Marker    Marker     `json:"regeneration"`
	Declined  []string   `json:"declined,omitempty"`
	Artefacts []Artefact `json:"artefacts"`
}

// Active returns the artefacts that apply to the tree, in render order.
func (s *Set) Active() []Artefact {
	active := make([]Artefact, 0, len(s.Artefacts))
	for index := 0; index < len(s.Artefacts); index++ {
		if s.Artefacts[index].Active {
			active = append(active, s.Artefacts[index])
		}
	}
	return active
}

// Resolve returns the artefacts manifest declares for tree: the built-in ones it does not
// decline, in their render order, then its own in declaration order, each with the files its
// paths select in tree. A declined name that is no built-in artefact, and a declared artefact
// that takes a built-in one's name, are refused. A nil manifest declares no section.
func Resolve(ctx context.Context, manifest *config.Manifest, tree Tree) (*Set, error) {
	if manifest == nil {
		manifest = &config.Manifest{}
	}
	files, err := tree.Files(ctx)
	if err != nil {
		return nil, err
	}
	builtins, err := builtinArtefacts(manifest)
	if err != nil {
		return nil, err
	}
	declined, err := declinedBuiltins(manifest.Generated, builtins)
	if err != nil {
		return nil, err
	}
	marker := manifest.Generated.Marker()
	set := &Set{Marker: Marker(marker)}
	for index := 0; index < len(builtins); index++ {
		if declined[builtins[index].decl.Name] {
			set.Declined = append(set.Declined, builtins[index].decl.Name)
			continue
		}
		artefact, err := builtins[index].resolve(ctx, tree, files)
		if err != nil {
			return nil, err
		}
		set.Artefacts = append(set.Artefacts, artefact)
	}
	declared, err := resolveDeclared(ctx, manifest.Generated, builtins, tree, files)
	if err != nil {
		return nil, err
	}
	set.Artefacts = append(set.Artefacts, declared...)
	return set, nil
}

// declinedBuiltins indexes generated.decline, refusing a name no built-in artefact carries: a
// misspelled decline would otherwise read as configured while the artefact stays guarded.
func declinedBuiltins(policy *config.GeneratedPolicy, builtins []builtin) (map[string]bool, error) {
	declined := map[string]bool{}
	if policy == nil {
		return declined, nil
	}
	known := make(map[string]bool, len(builtins))
	for index := 0; index < len(builtins); index++ {
		known[builtins[index].decl.Name] = true
	}
	for index := 0; index < len(policy.Decline) && index < config.MaxGeneratedDecline; index++ {
		name := policy.Decline[index]
		if !known[name] {
			return nil, fmt.Errorf("generated.decline[%d] %q names no built-in artefact; built in: %s",
				index, name, strings.Join(builtinNames(builtins), ", "))
		}
		declined[name] = true
	}
	return declined, nil
}

// resolveDeclared resolves the manifest's own artefacts; each always applies.
func resolveDeclared(ctx context.Context, policy *config.GeneratedPolicy, builtins []builtin, tree Tree, files []string) ([]Artefact, error) {
	if policy == nil {
		return nil, nil
	}
	reserved := make(map[string]bool, len(builtins))
	for index := 0; index < len(builtins); index++ {
		reserved[builtins[index].decl.Name] = true
	}
	artefacts := make([]Artefact, 0, len(policy.Artefacts))
	for index := 0; index < len(policy.Artefacts) && index < config.MaxGeneratedArtefacts; index++ {
		decl := policy.Artefacts[index]
		if reserved[decl.Name] {
			return nil, fmt.Errorf("generated.artefacts[%d] takes the name of the built-in artefact %q; "+
				"decline the built-in one under generated.decline and give yours another name", index, decl.Name)
		}
		artefact := newArtefact(decl, OriginManifest)
		if err := artefact.selectFiles(ctx, tree, files); err != nil {
			return nil, err
		}
		artefact.Active = true
		artefacts = append(artefacts, artefact)
	}
	return artefacts, nil
}

// newArtefact turns one declaration into an unresolved artefact.
func newArtefact(decl config.GeneratedArtefact, origin string) Artefact {
	artefact := Artefact{
		Name:    decl.Name,
		Origin:  origin,
		Paths:   append([]string(nil), decl.Paths...),
		Command: append([]string(nil), decl.Command...),
		Env:     decl.EnvList(),
		Sources: append([]string(nil), decl.Sources...),
		timeout: decl.RenderTimeout(),
		paths:   newGlobSet(decl.Paths),
		sources: newGlobSet(decl.Sources),
	}
	artefact.Timeout = artefact.timeout.String()
	if decl.Block != nil {
		artefact.Block = &Block{Start: decl.Block.Start, End: decl.Block.End}
	}
	return artefact
}

// selectFiles records the tree files the artefact's paths select; a block artefact keeps only
// those that carry its block. More than MaxArtefactFiles is an error, not a cut list.
func (a *Artefact) selectFiles(ctx context.Context, tree Tree, files []string) error {
	selected, err := a.paths.selectFrom(files, a.Name)
	if err != nil {
		return err
	}
	if a.Block == nil {
		a.Files = selected
		return nil
	}
	a.Files = make([]string, 0, len(selected))
	for index := 0; index < len(selected); index++ {
		content, exists, err := readText(ctx, tree, selected[index])
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		if _, found, err := extractBlock(content, a.Block); err != nil {
			return fmt.Errorf("%s: %s: %w", a.Name, selected[index], err)
		} else if exists && found {
			a.Files = append(a.Files, selected[index])
		}
	}
	return nil
}

// owns reports whether the artefact's paths select rel.
func (a *Artefact) owns(rel string) bool {
	return a.paths.match(rel)
}

// readsSource reports whether the artefact's sources select rel.
func (a *Artefact) readsSource(rel string) bool {
	return a.sources.match(rel)
}

// globSet is a list of repository-relative globs split into segments once, matched under
// util.MatchGlobSegments: "*" stays inside one segment and a "**" segment spans any number.
type globSet [][]string

// newGlobSet splits each glob into its segments.
func newGlobSet(globs []string) globSet {
	set := make(globSet, 0, len(globs))
	for index := 0; index < len(globs) && index < config.MaxGeneratedGlobs; index++ {
		set = append(set, strings.Split(globs[index], "/"))
	}
	return set
}

// match reports whether one glob matches the slash path rel in full.
func (g globSet) match(rel string) bool {
	segments := strings.Split(rel, "/")
	if len(segments) > maxPathSegments {
		return false
	}
	for index := 0; index < len(g); index++ {
		if util.MatchGlobSegments(g[index], segments) {
			return true
		}
	}
	return false
}

// selectFrom returns the files the set matches, sorted; owner names the artefact in the error
// that refuses more than MaxArtefactFiles.
func (g globSet) selectFrom(files []string, owner string) ([]string, error) {
	selected := make([]string, 0)
	for index := 0; index < len(files) && index < MaxTreeFiles; index++ {
		if !g.match(files[index]) {
			continue
		}
		if len(selected) == MaxArtefactFiles {
			return nil, fmt.Errorf("%s: its paths select more than %d files; narrow them", owner, MaxArtefactFiles)
		}
		selected = append(selected, files[index])
	}
	sort.Strings(selected)
	return selected, nil
}
