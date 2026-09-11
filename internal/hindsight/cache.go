// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MemoryDirRel  = ".workingdir/memory"
	MemoryFileRel = ".workingdir/memory/distilled.json"
)

// SaveLocalCache writes distilled memory facts to .workingdir/memory/distilled.json.
func SaveLocalCache(repoPath string, facts []MemoryFact) error {
	memDir := filepath.Join(repoPath, MemoryDirRel)
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return fmt.Errorf("failed to create memory directory: %w", err)
	}

	data, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal memory facts: %w", err)
	}

	filePath := filepath.Join(repoPath, MemoryFileRel)
	return os.WriteFile(filePath, data, 0644)
}

// LoadLocalCache reads distilled memory facts from .workingdir/memory/distilled.json.
func LoadLocalCache(repoPath string) ([]MemoryFact, error) {
	filePath := filepath.Join(repoPath, MemoryFileRel)
	if !util.FileExists(filePath) {
		return nil, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading local memory cache: %w", err)
	}

	var facts []MemoryFact
	if err := json.Unmarshal(data, &facts); err != nil {
		return nil, fmt.Errorf("failed unmarshaling memory facts: %w", err)
	}
	return facts, nil
}

// RecallLocalFacts performs instant in-memory keyword and category search over local facts.
func RecallLocalFacts(repoPath, query string, category FactCategory) []MemoryFact {
	facts, err := LoadLocalCache(repoPath)
	if err != nil || len(facts) == 0 {
		return nil
	}

	lowerQuery := strings.ToLower(query)
	var matches []MemoryFact

	for _, f := range facts {
		if category != "" && f.Category != category {
			continue
		}

		if lowerQuery == "" {
			matches = append(matches, f)
			continue
		}

		// Check subject, statement, or tags match
		if strings.Contains(strings.ToLower(f.Subject), lowerQuery) ||
			strings.Contains(strings.ToLower(f.Statement), lowerQuery) {
			matches = append(matches, f)
			continue
		}

		for _, tag := range f.Tags {
			if strings.Contains(strings.ToLower(tag), lowerQuery) {
				matches = append(matches, f)
				break
			}
		}
	}

	if len(matches) > 10 {
		return matches[:10]
	}
	return matches
}
