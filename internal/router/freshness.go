package router

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultCatalogMaxAgeDays is the freshness window when governance declares none.
	DefaultCatalogMaxAgeDays = 180
	// MaxCatalogMaxAgeDays bounds a declared window.
	MaxCatalogMaxAgeDays = 3650
	// SeedListDate is the date the built-in seed list was last edited. Seed entries carry it
	// as their as_of date: it dates the list, it does not claim a provider confirmed the entry.
	SeedListDate = "2026-10-08"
)

// CatalogFinding is one catalog entry the freshness policy flags.
type CatalogFinding struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

func (f CatalogFinding) String() string { return f.Model + ": " + f.Reason }

// CatalogMaxAge returns the freshness window of the catalog.
func CatalogMaxAge(cfg *RoutingConfig) time.Duration {
	days := cfg.Governance.CatalogMaxAgeDays
	if days <= 0 {
		days = DefaultCatalogMaxAgeDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// IsPreviewModel reports whether an entry is marked preview, by its flag or by a preview
// label in its model ID, the way providers name preview releases.
func IsPreviewModel(model ModelDescriptor) bool {
	return model.Preview || strings.Contains(strings.ToLower(model.ID), "preview")
}

// CatalogFindings lists, sorted by model ID, the entries that are marked preview or whose
// as_of date is older than the freshness window at now. An entry without an as_of date is not
// judged on age: nothing says when its data was written. The result is empty for a fresh
// catalog, never nil-versus-empty significant.
func CatalogFindings(cfg *RoutingConfig, now time.Time) []CatalogFinding {
	window := CatalogMaxAge(cfg)
	findings := make([]CatalogFinding, 0)
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
			findings = append(findings, entryFindings(tier.Models[i], now, window)...)
		}
	}
	slices.SortFunc(findings, func(a, b CatalogFinding) int {
		if byModel := strings.Compare(a.Model, b.Model); byModel != 0 {
			return byModel
		}
		return strings.Compare(a.Reason, b.Reason)
	})
	return findings
}

func entryFindings(model ModelDescriptor, now time.Time, window time.Duration) []CatalogFinding {
	var findings []CatalogFinding
	if IsPreviewModel(model) {
		findings = append(findings, CatalogFinding{Model: model.ID, Reason: "marked preview"})
	}
	if model.AsOf == "" {
		return findings
	}
	asOf, err := time.Parse(time.DateOnly, model.AsOf)
	if err != nil {
		return append(findings, CatalogFinding{Model: model.ID, Reason: fmt.Sprintf("as_of %q is not a YYYY-MM-DD date", model.AsOf)})
	}
	// Dates compare whole days: an entry is stale on the day after its window ends.
	if age := now.UTC().Truncate(24 * time.Hour).Sub(asOf); age > window {
		days := int(age / (24 * time.Hour))
		findings = append(findings, CatalogFinding{Model: model.ID,
			Reason: fmt.Sprintf("as_of %s is %d days old, beyond the %d-day window", model.AsOf, days, int(window/(24*time.Hour)))})
	}
	return findings
}
