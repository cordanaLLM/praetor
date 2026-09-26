// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"maps"
	"slices"
	"time"
)

// FactCategory classifies the semantic domain of a distilled memory fact.
type FactCategory string

const (
	CategoryGovernance       FactCategory = "governance"
	CategoryArchitecture     FactCategory = "architecture"
	CategoryCanonicalUtility FactCategory = "canonical_utility"
	CategoryBugRuling        FactCategory = "bug_ruling"
	CategoryDependencyDoc    FactCategory = "dependency_doc"
	CategoryFlavor           FactCategory = "flavor"
)

// MemoryFact is an atomic, verified truth extracted from Praetor intelligence subsystems.
type MemoryFact struct {
	ID        string       `json:"id"`
	Category  FactCategory `json:"category"`
	Subject   string       `json:"subject"`
	Statement string       `json:"statement"`
	Evidence  string       `json:"evidence"`
	Timestamp time.Time    `json:"timestamp"`
	Tags      []string     `json:"tags"`
}

// HindsightDocument represents the JSON payload format for Hindsight document ingestion.
type HindsightDocument struct {
	DocumentID string            `json:"document_id"`
	Content    string            `json:"content"`
	Context    string            `json:"context"`
	Tags       []string          `json:"tags"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// DistillationReport summarizes atomic facts harvested across repository AST and state.
// Warnings names every optional fact source that failed, so a report built from fewer
// sources than DistillWorkspace runs is distinguishable from a complete one.
type DistillationReport struct {
	TotalFacts  int                  `json:"total_facts"`
	Facts       []MemoryFact         `json:"facts"`
	Categories  map[FactCategory]int `json:"categories"`
	Warnings    []string             `json:"warnings,omitempty"`
	DistilledAt time.Time            `json:"distilled_at"`
}

// SortedCategories returns a category tally's keys in a stable order. Every caller that
// prints a tally uses it, so two runs over an unchanged repository produce the same
// report instead of one ordered by the runtime's map walk.
func SortedCategories(tally map[FactCategory]int) []FactCategory {
	return slices.Sorted(maps.Keys(tally))
}

// ClientConfig specifies network bounds and credentials for Hindsight API interactions.
type ClientConfig struct {
	BaseURL        string `json:"base_url"`
	Token          string `json:"token"`
	RateLimitRPM   int    `json:"rate_limit_rpm"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	OfflineOnly    bool   `json:"offline_only"`
}

// DefaultClientConfig provides rate-limited, fail-safe defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:        "http://localhost:8888",
		Token:          "",
		RateLimitRPM:   30,
		TimeoutSeconds: 5,
		OfflineOnly:    false,
	}
}

// PruneReport details stale facts identified and corresponding corrections.
type PruneReport struct {
	StaleCount  int      `json:"stale_count"`
	Corrections []string `json:"corrections"`
}
