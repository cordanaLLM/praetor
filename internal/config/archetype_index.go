package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// One extra directory entry permits the facets directory alongside 256 profiles.
const maxArchetypeDirectoryEntries = maxLockEntries + 1

// The same bounded index validates on-disk catalogs and prospective replacements.
// Snapshot keys are absolute paths, selected and validated by the projection API.
func indexArchetypesWithSnapshots(ctx context.Context, root, dir string, snapshots map[string][]byte, missingOK bool) (map[string]string, error) {
	entries, err := readArchetypeEntries(ctx, dir)
	if err != nil && (!missingOK || !errors.Is(err, os.ErrNotExist)) {
		return nil, err
	}
	if err := validateProspectiveEntryCount(dir, entries, snapshots); err != nil {
		return nil, err
	}
	names, err := prospectiveArchetypeNames(dir, entries, snapshots)
	if err != nil {
		return nil, err
	}
	index := make(map[string]string, len(names))
	for i := 0; i < len(names) && i < maxLockEntries; i++ {
		path, err := confinedArchetypePath(root, dir, names[i])
		if err != nil {
			return nil, err
		}
		id, err := indexedArchetypeID(ctx, path, snapshots)
		if err != nil {
			return nil, err
		}
		if _, duplicate := index[id]; duplicate {
			return nil, fmt.Errorf("duplicate archetype ID %q in %s", id, dir)
		}
		index[id] = path
	}
	return index, nil
}

func readArchetypeEntries(ctx context.Context, path string) (entries []os.DirEntry, err error) {
	root, err := contextopt.OpenDirectory(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read archetype directory %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err = directory.ReadDir(maxArchetypeDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxArchetypeDirectoryEntries {
		return nil, fmt.Errorf("archetype directory exceeds %d entries", maxArchetypeDirectoryEntries)
	}
	return entries, ctx.Err()
}

func prospectiveArchetypeNames(dir string, entries []os.DirEntry, snapshots map[string][]byte) ([]string, error) {
	names := make(map[string]bool, len(entries))
	for i := 0; i < len(entries) && i < maxArchetypeDirectoryEntries; i++ {
		entry := entries[i]
		_, replaced := snapshots[filepath.Join(dir, entry.Name())]
		if replaced && !entry.Type().IsRegular() {
			return nil, fmt.Errorf("catalog replacement must target a regular file: %s", entry.Name())
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names[entry.Name()] = true
		}
	}
	for path := range snapshots {
		if filepath.Dir(path) == dir {
			names[filepath.Base(path)] = true
		}
	}
	if len(names) > maxLockEntries {
		return nil, fmt.Errorf("archetype index exceeds %d YAML files", maxLockEntries)
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func validateProspectiveEntryCount(dir string, entries []os.DirEntry, snapshots map[string][]byte) error {
	names := make(map[string]bool, len(entries))
	for i := 0; i < len(entries) && i < maxArchetypeDirectoryEntries; i++ {
		names[entries[i].Name()] = true
	}
	for path := range snapshots {
		parent := filepath.Dir(path)
		if parent == dir {
			names[filepath.Base(path)] = true
		} else if filepath.Dir(parent) == dir {
			// A planned facet also creates its parent entry in the profile index.
			names[filepath.Base(parent)] = true
		}
	}
	if len(names) > maxArchetypeDirectoryEntries {
		return fmt.Errorf("prospective archetype directory exceeds %d entries", maxArchetypeDirectoryEntries)
	}
	return nil
}

func indexedArchetypeID(ctx context.Context, path string, snapshots map[string][]byte) (string, error) {
	data, ok := snapshots[path]
	if !ok {
		var err error
		data, err = contextopt.ReadSnapshot(ctx, path)
		if err != nil {
			return "", err
		}
	}
	return parseArchetypeID(ctx, path, data)
}
