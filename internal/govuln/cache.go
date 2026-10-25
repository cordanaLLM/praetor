// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

const (
	// CacheMissNote names recovery for order engine-gate-private-gocache-20261025.
	CacheMissNote = "go-build cache miss, retried once"
	// cacheMissPath and cacheMissError must occur on the same diagnostic line. Measured
	// example: could not import fmt (open ~/.cache/go-build/ab/entry-d: no such file or directory).
	// Both archive (-a) and compiled-data (-d) entries can disappear during concurrent pruning.
	cacheMissPath           = "/go-build/"
	cacheMissError          = "no such file or directory"
	maxCacheDiagnosticLines = 100000
)

// RetryCacheMiss retries a failed Go command exactly once on the concurrent-prune signature
// from order engine-gate-private-gocache-20261025. It preserves the command, context and second
// result, never retries cancellation or unrelated errors, and calls note after the retry.
// Standalone govulncheck also loads packages through Go; other scanner commands are excluded.
// Callers wrap once at the command boundary and must supply note to make the retry visible.
func RetryCacheMiss(run Runner, note func(dir string) error) Runner {
	return func(ctx context.Context, dir, name string, args ...string) (string, error) {
		out, err := run(ctx, dir, name, args...)
		if err == nil || ctx.Err() != nil || !cacheMissCommand(name) {
			return out, err
		}
		if !isCacheMiss(out) && !isCacheMiss(err.Error()) {
			return out, err
		}
		out, err = run(ctx, dir, name, args...)
		if noteErr := note(dir); noteErr != nil {
			return out, errors.Join(err, noteErr)
		}
		return out, err
	}
}

func cacheMissCommand(name string) bool {
	switch filepath.Base(name) {
	case "go", "go.exe", DefaultScanner, DefaultScanner + ".exe":
		return true
	default:
		return false
	}
}

func isCacheMiss(text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\\", "/"), "\n")
	for i := 0; i < len(lines) && i < maxCacheDiagnosticLines; i++ {
		if strings.Contains(lines[i], cacheMissPath) && strings.Contains(lines[i], cacheMissError) {
			return true
		}
	}
	return false
}
