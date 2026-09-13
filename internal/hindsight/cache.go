// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MemoryDirRel  = ".workingdir/memory"
	MemoryFileRel = ".workingdir/memory/distilled.json"
)

// SaveLocalCache writes distilled memory facts to .workingdir/memory/distilled.json.
func SaveLocalCache(repoPath string, facts []MemoryFact) error {
	return SaveLocalCacheContext(context.Background(), repoPath, facts)
}

// SaveLocalCacheContext atomically publishes bounded private facts without following links.
func SaveLocalCacheContext(ctx context.Context, repoPath string, facts []MemoryFact) error {
	data, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory facts: %w", err)
	}
	if len(data) > contextopt.MaxSourceBytes {
		return fmt.Errorf("memory cache exceeds %d bytes", contextopt.MaxSourceBytes)
	}
	path, err := util.ConfinePath(repoPath, MemoryFileRel)
	if err != nil {
		return err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(path), 0700); err != nil {
		return err
	}
	before, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: err == nil, Mode: 0600})
}

// LoadLocalCache reads distilled memory facts from .workingdir/memory/distilled.json.
func LoadLocalCache(repoPath string) ([]MemoryFact, error) {
	return LoadLocalCacheContext(context.Background(), repoPath)
}

// LoadLocalCacheContext reads a complete, bounded local fact cache under caller context.
func LoadLocalCacheContext(ctx context.Context, repoPath string) ([]MemoryFact, error) {
	if ctx == nil {
		return nil, fmt.Errorf("memory cache read requires context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	filePath, err := util.ConfinePath(repoPath, MemoryFileRel)
	if err != nil {
		return nil, fmt.Errorf("resolve local memory cache: %w", err)
	}
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect local memory cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local memory cache must be a regular file: %s", filePath)
	}

	data, err := contextopt.ReadSnapshot(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading local memory cache: %w", err)
	}

	var facts []MemoryFact
	if err := json.Unmarshal(data, &facts); err != nil {
		return nil, fmt.Errorf("failed unmarshaling memory facts: %w", err)
	}
	return facts, nil
}

// RecallLocalFacts preserves the legacy best-effort query API, returning no facts
// when the cache cannot be read. Authoritative callers must use RecallLocalFactsWithError.
func RecallLocalFacts(repoPath, query string, category FactCategory) []MemoryFact {
	facts, err := RecallLocalFactsWithError(repoPath, query, category)
	if err != nil {
		return nil
	}
	return facts
}

// RecallLocalFactsWithError searches cached facts without hiding cache failures.
// A missing cache is a legitimate empty result; corrupt or unreadable data is an error.
func RecallLocalFactsWithError(repoPath, query string, category FactCategory) ([]MemoryFact, error) {
	return RecallLocalFactsContext(context.Background(), repoPath, query, category)
}

// RecallLocalFactsContext preserves caller cancellation and incomplete-cache failures.
func RecallLocalFactsContext(ctx context.Context, repoPath, query string, category FactCategory) ([]MemoryFact, error) {
	facts, err := LoadLocalCacheContext(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("recall local memory facts: %w", err)
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
		return matches[:10], nil
	}
	return matches, nil
}
