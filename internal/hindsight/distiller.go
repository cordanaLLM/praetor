// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/docdistill"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/state"
)

// DistillWorkspace harvests verified truth across Praetor subsystems into atomic facts.
func DistillWorkspace(ctx context.Context, repoPath string) (*DistillationReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("hindsight distill: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hindsight distill cancelled: %w", err)
	}

	var facts []MemoryFact
	categories := make(map[FactCategory]int)

	// 1. Flavor Archetype Facts
	flavorFacts, err := distillFlavorFacts(ctx, repoPath)
	if err == nil {
		facts = append(facts, flavorFacts...)
	}

	// 2. Session State & Bug Ledger Facts
	stateFacts, err := distillStateFacts(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("distill workspace bug ledger: %w", err)
	}
	facts = append(facts, stateFacts...)

	// 3. Deduplication & Canonical Utility Facts
	dedupeFacts, err := distillDedupeFacts(ctx, repoPath)
	if err == nil {
		facts = append(facts, dedupeFacts...)
	}

	// 4. Distilled Package Documentation Facts
	docFacts, err := distillPackageDocFacts(repoPath)
	if err == nil {
		facts = append(facts, docFacts...)
	}

	// Tally categories
	for _, f := range facts {
		categories[f.Category]++
	}

	return &DistillationReport{
		TotalFacts:  len(facts),
		Facts:       facts,
		Categories:  categories,
		DistilledAt: time.Now().UTC(),
	}, nil
}

func distillFlavorFacts(ctx context.Context, repoPath string) ([]MemoryFact, error) {
	var facts []MemoryFact
	detected := flavor.DetectFlavor(repoPath)
	if detected != "" {
		flv, err := flavor.Get(detected)
		if err == nil {
			stmt := fmt.Sprintf("Repository is governed under archetype %s (Profile: %s). Description: %s.",
				flv.Name(), flv.HISSProfile(), flv.Description())
			facts = append(facts, createFact(CategoryFlavor, flv.Name(), stmt, "internal/flavor", []string{"flavor", flv.Name()}))
		}
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
		return nil, err
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
		return nil, err
	}

	for _, doc := range cat.Packages {
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
