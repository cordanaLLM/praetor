// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/docdistill"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/state"
)

// DistillWorkspace harvests verified truth across Praetor subsystems into atomic facts.
//
// The bug ledger is a required source: a ledger that cannot be read aborts the
// distillation. The flavor, dedupe and package-doc sources are optional: a failing one is
// recorded in the report's Warnings and the remaining sources still run, so a partial
// result states what it is missing instead of passing for a complete one. When sources
// failed and nothing was harvested, DistillWorkspace returns an error rather than an empty
// report that would replace a populated cache with zero facts.
func DistillWorkspace(ctx context.Context, repoPath string) (*DistillationReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("hindsight distill: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hindsight distill cancelled: %w", err)
	}
	return distillSources(ctx, repoPath, workspaceSources())
}

// distillSource is one fact source DistillWorkspace harvests.
type distillSource struct {
	name     string
	required bool
	run      func(ctx context.Context, repoPath string) ([]MemoryFact, error)
}

// workspaceSources lists the fact sources in the order their facts appear in the report.
func workspaceSources() []distillSource {
	return []distillSource{
		{name: "flavor archetype", run: distillFlavorFacts},
		{name: "bug ledger", required: true, run: distillStateFacts},
		{name: "dedupe", run: distillDedupeFacts},
		{name: "package docs", run: func(_ context.Context, repoPath string) ([]MemoryFact, error) {
			return distillPackageDocFacts(repoPath)
		}},
	}
}

// distillSources runs every source once, so the loop is bounded by the fixed source list
// (HISS-02).
func distillSources(ctx context.Context, repoPath string, sources []distillSource) (*DistillationReport, error) {
	var facts []MemoryFact
	var failures []error
	for _, source := range sources {
		sourceFacts, err := source.run(ctx, repoPath)
		if err == nil {
			facts = append(facts, sourceFacts...)
			continue
		}
		wrapped := fmt.Errorf("distill workspace %s: %w", source.name, err)
		if source.required {
			return nil, wrapped
		}
		failures = append(failures, wrapped)
	}
	if len(facts) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("hindsight distill harvested no facts: %w", errors.Join(failures...))
	}

	categories := make(map[FactCategory]int)
	for _, f := range facts {
		categories[f.Category]++
	}
	warnings := make([]string, 0, len(failures))
	for _, failure := range failures {
		warnings = append(warnings, failure.Error())
	}

	return &DistillationReport{
		TotalFacts:  len(facts),
		Facts:       facts,
		Categories:  categories,
		Warnings:    warnings,
		DistilledAt: time.Now().UTC(),
	}, nil
}

func distillFlavorFacts(ctx context.Context, repoPath string) ([]MemoryFact, error) {
	var facts []MemoryFact
	detected := flavor.DetectFlavor(repoPath)
	if detected != "" {
		flv, err := flavor.Get(detected)
		if err != nil {
			return nil, fmt.Errorf("resolve detected flavor %q: %w", detected, err)
		}
		stmt := fmt.Sprintf("Repository is governed under archetype %s (Profile: %s). Description: %s.",
			flv.Name(), flv.HISSProfile(), flv.Description())
		facts = append(facts, createFact(CategoryFlavor, flv.Name(), stmt, "internal/flavor", []string{"flavor", flv.Name()}))
	}
	return facts, nil
}

func distillStateFacts(ctx context.Context, repoPath string) ([]MemoryFact, error) {
	var facts []MemoryFact

	bugs, err := state.ListBugsContext(ctx, repoPath, "resolved")
	if err != nil {
		return nil, fmt.Errorf("read resolved bug ledger: %w", err)
	}
	for _, b := range bugs {
		stmt := fmt.Sprintf("Resolved Bug %s (%s): %s.", b.ID, b.Severity, b.Title)
		facts = append(facts, createFact(CategoryBugRuling, b.ID, stmt, ".workingdir/BUGS.md", []string{"bug", b.Severity}))
	}

	return facts, nil
}

func distillDedupeFacts(ctx context.Context, repoPath string) ([]MemoryFact, error) {
	var facts []MemoryFact
	report, err := dedupe.ScanRepoContext(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("scan repository clones: %w", err)
	}

	stmt := fmt.Sprintf("Codebase AST deduplication cleanliness score is %.1f%% across %d functions (%d files).",
		report.CleanlinessScore, report.TotalFuncsScanned, report.TotalFilesScanned)
	facts = append(facts, createFact(CategoryCanonicalUtility, "ast_dedupe", stmt, "internal/dedupe", []string{"dedupe", "ast"}))

	return facts, nil
}

func distillPackageDocFacts(repoPath string) ([]MemoryFact, error) {
	var facts []MemoryFact
	cat, err := docdistill.LoadCatalog(repoPath)
	if err != nil {
		return nil, fmt.Errorf("load package doc catalog: %w", err)
	}

	// Sorted, so the distilled cache is byte-identical between two runs over an unchanged
	// catalog instead of diffing against itself on every distillation.
	for _, key := range cat.SortedKeys() {
		doc := cat.Packages[key]
		stmt := fmt.Sprintf("Package %s@%s: %s (API signatures: %d, Invariants: %d).",
			doc.PackageName, doc.Version, doc.Summary, len(doc.APISurface), len(doc.Invariants))
		facts = append(facts, createFact(CategoryDependencyDoc, doc.PackageName, stmt, ".workingdir/docs/catalog.json", []string{"package", doc.PackageName}))
	}

	return facts, nil
}

func createFact(cat FactCategory, subject, statement, evidence string, tags []string) MemoryFact {
	raw := fmt.Sprintf("%s:%s:%s", cat, subject, statement)
	hash := sha256.Sum256([]byte(raw))
	id := hex.EncodeToString(hash[:16])

	return MemoryFact{
		ID:        id,
		Category:  cat,
		Subject:   subject,
		Statement: statement,
		Evidence:  evidence,
		Timestamp: time.Now().UTC(),
		Tags:      tags,
	}
}
