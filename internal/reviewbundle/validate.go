// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import "fmt"

// Validate checks retained budgets, cut marks and coverage of ConflictedPaths using Limits (or defaults).
// It performs no Git I/O: a tree hash alone cannot reveal omitted conflict evidence or prove provenance.
func Validate(b Bundle) error {
	if err := validateMetadata(b); err != nil {
		return err
	}
	limits, err := normalizeOptions(b.Limits)
	if err != nil {
		return err
	}
	if len(b.Files) > limits.MaxFiles {
		return fmt.Errorf("bundle exceeds %d files", limits.MaxFiles)
	}
	if err := validateCuts(b); err != nil {
		return err
	}
	total := 0
	for i := 0; i < MaxMetadataEntries && i < len(b.Files); i++ {
		if err := validateFile(b.Files[i], limits.MaxFileBytes); err != nil {
			return err
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
	if err := validateMetadata(b); err != nil {
		return err
	}
	classified := make(map[string]bool, len(b.Conflicts))
	for i := 0; i < MaxMetadataEntries && i < len(b.Conflicts); i++ {
		c := b.Conflicts[i]
		if c.Path == "" || c.Kind == "" {
			return fmt.Errorf("conflict needs path and kind")
		}
		classified[c.Path] = true
	}
	for i := 0; i < MaxMetadataEntries && i < len(b.ConflictedPaths); i++ {
		if !classified[b.ConflictedPaths[i]] {
			return fmt.Errorf("conflicted path %q missing from Conflicts", b.ConflictedPaths[i])
		}
	}
	return nil
}

// validateMetadata rejects oversized inventories before any allocation or inventory loop.
func validateMetadata(b Bundle) error {
	counts := [...]int{len(b.Files), len(b.Cut), len(b.OmittedPaths), len(b.Conflicts), len(b.ConflictedPaths)}
	for i := 0; i < len(counts); i++ {
		if counts[i] > MaxMetadataEntries {
			return fmt.Errorf("bundle metadata exceeds %d entries", MaxMetadataEntries)
		}
	}
	return nil
}

// validateCuts accounts for every cut exactly once, as either a retained cut or an omitted path.
func validateCuts(b Bundle) error {
	cut := make(map[string]bool, len(b.Cut))
	for i := 0; i < len(b.Cut); i++ {
		path := b.Cut[i]
		if path == "" || cut[path] {
			return fmt.Errorf("empty or duplicate Cut path %q", path)
		}
		cut[path] = true
	}
	seen, err := validateRetainedCuts(b.Files, cut)
	if err != nil {
		return err
	}
	for i := 0; i < len(b.OmittedPaths); i++ {
		path := b.OmittedPaths[i]
		if path == "" || seen[path] || !cut[path] {
			return fmt.Errorf("omitted path %q needs exactly one Cut entry and no retained file", path)
		}
		seen[path] = true
		delete(cut, path)
	}
	if len(cut) != 0 {
		return fmt.Errorf("unaccounted Cut entries")
	}
	return nil
}

// validateRetainedCuts consumes retained cuts and refuses duplicate file paths.
// Validate checks both inventory sizes against MaxMetadataEntries before this call.
func validateRetainedCuts(files []File, cut map[string]bool) (map[string]bool, error) {
	seen := make(map[string]bool, len(files))
	for i := 0; i < len(files); i++ {
		f := files[i]
		if seen[f.Path] || f.Truncated != cut[f.Path] {
			return nil, fmt.Errorf("file %q has duplicate path or inconsistent Cut mark", f.Path)
		}
		seen[f.Path] = true
		delete(cut, f.Path)
	}
	return seen, nil
}
