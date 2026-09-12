package notebook

import (
	"context"
	"fmt"
	"strings"
)

type Requirement struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	SourceID     string `json:"source_id"`
	SourceSHA256 string `json:"source_sha256"`
	Quote        string `json:"quote"`
}

type Generation struct {
	BundleSHA256 string            `json:"bundle_sha256"`
	Requirements []Requirement     `json:"requirements"`
	Documents    map[string]string `json:"documents"`
}

// ValidateGeneration checks structural grounding, never semantic truth or approval.
func ValidateGeneration(ctx context.Context, bundleRaw, resultRaw []byte) (*Generation, error) {
	if _, err := Prepare(ctx, bundleRaw); err != nil {
		return nil, err
	}
	var b Bundle
	if err := Decode(bundleRaw, &b); err != nil {
		return nil, err
	}
	var g Generation
	if err := Decode(resultRaw, &g); err != nil {
		return nil, err
	}
	if g.BundleSHA256 != Digest(bundleRaw) || len(g.Requirements) == 0 || len(g.Requirements) > 256 {
		return nil, fmt.Errorf("generation requires matching snapshot and 1..256 requirements")
	}
	sources := make(map[string]Source)
	for _, s := range b.Sources {
		sources[s.ID] = s
	}
	seen := make(map[string]bool)
	for _, r := range g.Requirements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateRequirement(r, sources, seen); err != nil {
			return nil, err
		}
	}
	if err := validateDocuments(g.Documents); err != nil {
		return nil, err
	}
	return &g, nil
}

func validateRequirement(r Requirement, sources map[string]Source, seen map[string]bool) error {
	if !identifier(r.ID) || seen[r.ID] || !label(r.Text) {
		return fmt.Errorf("invalid or duplicate requirement")
	}
	s, ok := sources[r.SourceID]
	if !ok || s.SHA256 != r.SourceSHA256 || len(strings.TrimSpace(r.Quote)) == 0 || len(r.Quote) > 4096 || !strings.Contains(s.Content, r.Quote) {
		return fmt.Errorf("requirement %s has an invalid source citation", r.ID)
	}
	seen[r.ID] = true
	return nil
}

func validateDocuments(documents map[string]string) error {
	if len(documents) != 3 {
		return fmt.Errorf("exactly three planning documents required")
	}
	for _, key := range []string{"project_plan", "specification", "tasks"} {
		text := documents[key]
		if strings.TrimSpace(text) == "" || len(text) > MaxContentBytes || strings.ContainsRune(text, 0) {
			return fmt.Errorf("missing or oversized %s document", key)
		}
	}
	return nil
}
