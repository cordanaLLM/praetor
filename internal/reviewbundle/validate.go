// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import "fmt"

// Validate checks retained budgets, cut marks and coverage of ConflictedPaths using Limits (or defaults).
// It performs no Git I/O: a tree hash alone cannot reveal omitted conflict evidence or prove provenance.
func Validate(b Bundle) error {
	limits, err := normalizeOptions(b.Limits)
	if err != nil {
		return err
	}
	if len(b.Files) > limits.MaxFiles {
		return fmt.Errorf("bundle exceeds %d files", limits.MaxFiles)
	}
	cut := make(map[string]bool, len(b.Cut))
	for i := 0; i < len(b.Cut); i++ {
		cut[b.Cut[i]] = true
	}
	total := 0
	for i := 0; i < len(b.Files); i++ {
		if err := validateFile(b.Files[i], limits.MaxFileBytes); err != nil {
			return err
		}
		if b.Files[i].Truncated != cut[b.Files[i].Path] {
			return fmt.Errorf("file %q has inconsistent TRUNCATED mark and Cut entry", b.Files[i].Path)
		}
		total += len(b.Files[i].Content)
		if total > limits.MaxBundleBytes {
			return fmt.Errorf("bundle exceeds %d content bytes", limits.MaxBundleBytes)
		}
	}
	return validateConflicts(b)
}

// validateFile refuses unmarked oversized content and inconsistent file-state fields.
func validateFile(f File, maxBytes int) error {
	if f.Path == "" {
		return fmt.Errorf("bundle file has empty path")
	}
	if len(f.Content) > maxBytes {
		return fmt.Errorf("file %q exceeds %d bytes (TRUNCATED: %t)", f.Path, maxBytes, f.Truncated)
	}
	if f.Truncated != (f.TruncatedBytes > 0) || f.TruncatedBytes < 0 {
		return fmt.Errorf("file %q has inconsistent TRUNCATED mark and omitted byte count", f.Path)
	}
	if (f.Deleted || f.Binary) && (f.Content != "" || f.Truncated) {
		return fmt.Errorf("deleted or binary file %q carries content or a cut", f.Path)
	}
	return nil
}

// validateConflicts requires a kind per conflict and classification for every retained conflict path.
func validateConflicts(b Bundle) error {
	classified := make(map[string]bool, len(b.Conflicts))
	for i := 0; i < len(b.Conflicts); i++ {
		c := b.Conflicts[i]
		if c.Path == "" || c.Kind == "" {
			return fmt.Errorf("conflict needs path and kind")
		}
		classified[c.Path] = true
	}
	for i := 0; i < len(b.ConflictedPaths); i++ {
		if !classified[b.ConflictedPaths[i]] {
			return fmt.Errorf("conflicted path %q missing from Conflicts", b.ConflictedPaths[i])
		}
	}
	return nil
}
