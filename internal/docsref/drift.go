// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds of one drift check (HISS-02).
const (
	// MaxDriftPaths bounds the changed paths one check reads. It equals the bound of
	// cifilter.GetChangedFiles, which reads the diff first.
	MaxDriftPaths = 5000
	// maxNamedPaths bounds the changed paths one finding names.
	maxNamedPaths = 3
	// maxWaiversPerText bounds the waivers read from one commit message or body.
	maxWaiversPerText = 16
	// maxWaiverReasonBytes bounds the reason a waiver line prints.
	maxWaiverReasonBytes = 240
)

// decisionRecords holds the decision records, which never count as documentation: a record
// states why a decision was taken, not how the surface behaves now.
const decisionRecords = "docs/adr/"

// commitWaiver matches a Docs-Waiver: trailer line and captures its reason.
var commitWaiver = regexp.MustCompile(`(?im)^[ \t]*Docs-Waiver:[ \t]*(\S[^\r\n]*)$`)

// bodyWaiver matches a "no docs needed: <reason>" statement and captures its reason.
var bodyWaiver = regexp.MustCompile(`(?i)no docs needed[: ][ \t]*(\S[^\r\n]*)`)

// Waiver is one reasoned statement that a change needs no documentation edit.
type Waiver struct {
	// Source says where the waiver is written: a commit of the range or the pull request body.
	Source string
	// Reason is the waiver's stated reason.
	Reason string
}

// String renders the waiver as its source and reason.
func (w Waiver) String() string {
	return w.Source + ": " + w.Reason
}

// DriftReport is the outcome of one drift check. The counts print on a clean run too, so a
// run that read nothing is told apart from one that found nothing.
type DriftReport struct {
	Surfaces   int
	Paths      int
	Documented []string
	Waivers    []Waiver
	Waived     []string
	Findings   []string
}

// CommitWaivers returns the Docs-Waiver: trailers of one commit message; commit names the
// commit in each waiver's source. A trailer without a reason waives nothing.
func CommitWaivers(commit, message string) []Waiver {
	return waivers("commit "+commit, message, commitWaiver)
}

// BodyWaivers returns the "no docs needed: <reason>" statements of a pull request body. A
// statement without a reason waives nothing.
func BodyWaivers(body string) []Waiver {
	return waivers("pull request body", body, bodyWaiver)
}

// waivers returns every reasoned match of pattern in text.
func waivers(source, text string, pattern *regexp.Regexp) []Waiver {
	var found []Waiver
	for _, match := range pattern.FindAllStringSubmatch(text, maxWaiversPerText) {
		reason := util.TruncateExcerpt(strings.TrimSpace(match[1]), maxWaiverReasonBytes)
		found = append(found, Waiver{Source: source, Reason: reason})
	}
	return found
}

// Drift checks one change set against the declared surfaces. A surface a changed path belongs
// to needs a changed path matching its docs; a surface without docs is reported as unmapped.
// With a waiver every such finding is admitted and listed as waived instead.
func Drift(changed []string, surfaces []config.DocsSurface, waived []Waiver) (*DriftReport, error) {
	if len(changed) > MaxDriftPaths {
		return nil, fmt.Errorf("the change touches %d paths; the drift check reads at most %d", len(changed), MaxDriftPaths)
	}
	report := &DriftReport{Surfaces: len(surfaces), Paths: len(changed), Waivers: waived}
	for index := 0; index < len(surfaces) && index < config.MaxDocsSurfaces; index++ {
		finding, documented := surfaceDrift(surfaces[index], changed)
		switch {
		case documented != "":
			report.Documented = append(report.Documented, surfaces[index].Name+": "+documented)
		case finding == "":
		case len(waived) > 0:
			report.Waived = append(report.Waived, finding)
		default:
			report.Findings = append(report.Findings, finding)
		}
	}
	return report, nil
}

// surfaceDrift returns the finding for one surface, or the changed document that satisfies
// it, or neither when the change does not touch the surface.
func surfaceDrift(surface config.DocsSurface, changed []string) (finding, documented string) {
	var touched []string
	for _, rel := range changed {
		if SurfaceFile(surface, rel) {
			touched = append(touched, rel)
		}
	}
	if len(touched) == 0 {
		return "", ""
	}
	if len(surface.Docs) == 0 {
		return fmt.Sprintf("%s: %s changed, and the surface is unmapped: its docs_surfaces entry lists no documentation",
			surface.Name, namedPaths(touched)), ""
	}
	for _, rel := range changed {
		if Documents(surface, rel) {
			return "", rel
		}
	}
	return fmt.Sprintf("%s: %s changed without an edit to %s", surface.Name, namedPaths(touched), strings.Join(surface.Docs, " or ")), ""
}

// SurfaceFile reports whether the repository path rel belongs to surface: a paths glob
// matches it and no exclude glob does.
func SurfaceFile(surface config.DocsSurface, rel string) bool {
	return matchesAny(surface.Paths, rel) && !matchesAny(surface.Exclude, rel)
}

// Documents reports whether editing the repository path rel counts as editing the surface's
// documentation: a docs glob matches it, and it is neither a decision record nor a file of the
// surface itself.
func Documents(surface config.DocsSurface, rel string) bool {
	return !strings.HasPrefix(rel, decisionRecords) && !SurfaceFile(surface, rel) && matchesAny(surface.Docs, rel)
}

// matchesAny reports whether one of globs matches the slash path rel in full.
func matchesAny(globs []string, rel string) bool {
	segments := strings.Split(rel, "/")
	if len(segments) > maxPathSegments {
		return false
	}
	for index := 0; index < len(globs) && index < config.MaxDocsSurfaceGlobs; index++ {
		if util.MatchGlobSegments(strings.Split(globs[index], "/"), segments) {
			return true
		}
	}
	return false
}

// namedPaths lists the first changed paths of a finding and counts the rest.
func namedPaths(paths []string) string {
	if len(paths) <= maxNamedPaths {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:maxNamedPaths], ", "), len(paths)-maxNamedPaths)
}

// CheckSurfaces reports every glob of the declared surfaces that selects nothing in the
// repository at root, which must be the top of its git work tree. A paths glob that matches
// no file guards nothing, and a docs glob that matches no document can never be satisfied, so
// every change to its surface would fail with no way to clear it.
func CheckSurfaces(ctx context.Context, root string, surfaces []config.DocsSurface) ([]string, error) {
	_, inventory, err := loadIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	var problems []string
	for index := 0; index < len(surfaces) && index < config.MaxDocsSurfaces; index++ {
		problems = append(problems, surfaceProblems(surfaces[index], inventory)...)
	}
	return problems, nil
}

// surfaceProblems checks one surface's paths and docs globs against the inventory.
func surfaceProblems(surface config.DocsSurface, inventory []string) []string {
	var problems []string
	for index := 0; index < len(surface.Paths) && index < config.MaxDocsSurfaceGlobs; index++ {
		glob := surface.Paths[index]
		if !selects(inventory, func(rel string) bool { return matchesAny([]string{glob}, rel) && SurfaceFile(surface, rel) }) {
			problems = append(problems, fmt.Sprintf("%s: paths glob %q selects no file of the repository", surface.Name, glob))
		}
	}
	for index := 0; index < len(surface.Docs) && index < config.MaxDocsSurfaceGlobs; index++ {
		glob := surface.Docs[index]
		mapped := config.DocsSurface{Name: surface.Name, Paths: surface.Paths, Exclude: surface.Exclude, Docs: []string{glob}}
		if !selects(inventory, func(rel string) bool { return Documents(mapped, rel) }) {
			problems = append(problems, fmt.Sprintf("%s: docs glob %q matches no document of the repository (decision records under %s never count)",
				surface.Name, glob, decisionRecords))
		}
	}
	return problems
}

// selects reports whether keep accepts one path of the inventory.
func selects(inventory []string, keep func(string) bool) bool {
	for index := 0; index < len(inventory) && index < maxInventoryEntries; index++ {
		if keep(inventory[index]) {
			return true
		}
	}
	return false
}
