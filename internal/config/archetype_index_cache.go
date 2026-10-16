// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// archetypeIndexCacheKey keys the cache WithArchetypeIndexCache puts on a context.
type archetypeIndexCacheKey struct{}

// archetypeIndexCache holds archetypeSources' indexes per catalog root under one context, each
// with the fingerprint of the directories it was built from.
type archetypeIndexCache struct {
	mu      sync.Mutex
	entries map[string]archetypeIndexEntry
}

// archetypeIndexEntry is one root's indexes and the fingerprint they were built from.
type archetypeIndexEntry struct {
	fingerprint      string
	profiles, facets map[string]string
}

// WithArchetypeIndexCache returns a context under which archetypeSources builds a catalog
// root's profile and facet indexes once and answers later calls for that root from them, as long
// as the two directories still list the same files with the same sizes and modification times.
// One adoption indexed the same catalog three times (effective policy pins, lock verification and
// the lock catalog hash), each time confining and reading every archetype file, which costs a
// noticeable part of an adoption on Windows (#1382). Another root, or a file added, removed or
// rewritten in it, is indexed afresh.
func WithArchetypeIndexCache(ctx context.Context) context.Context {
	if _, cached := ctx.Value(archetypeIndexCacheKey{}).(*archetypeIndexCache); cached {
		return ctx
	}
	return context.WithValue(ctx, archetypeIndexCacheKey{}, &archetypeIndexCache{entries: map[string]archetypeIndexEntry{}})
}

// cachedArchetypeSources is archetypeSources through the context's cache, when there is one and
// the root's directories can be fingerprinted; otherwise it indexes as archetypeSources always did.
func cachedArchetypeSources(ctx context.Context, rootDir string, build func() (map[string]string, map[string]string, error)) (map[string]string, map[string]string, error) {
	cache, _ := ctx.Value(archetypeIndexCacheKey{}).(*archetypeIndexCache)
	if cache == nil {
		return build()
	}
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return build()
	}
	fingerprint, ok := archetypeFingerprint(root)
	if !ok {
		return build()
	}
	cache.mu.Lock()
	entry, known := cache.entries[root]
	cache.mu.Unlock()
	if known && entry.fingerprint == fingerprint {
		return maps.Clone(entry.profiles), maps.Clone(entry.facets), nil
	}
	profiles, facets, err := build()
	if err != nil {
		return nil, nil, err
	}
	cache.mu.Lock()
	cache.entries[root] = archetypeIndexEntry{fingerprint: fingerprint, profiles: maps.Clone(profiles), facets: maps.Clone(facets)}
	cache.mu.Unlock()
	return profiles, facets, nil
}

// archetypeFingerprint lists the archetype and facet directories below root: every entry's name,
// type, size and modification time, or "absent" for a missing directory. ok is false when either
// directory is a link or cannot be listed: such a root is not cached, and the confined index
// refuses or reports it as before.
func archetypeFingerprint(root string) (string, bool) {
	var b strings.Builder
	for _, rel := range []string{archetypeDirName, filepath.Join(archetypeDirName, facetDirName)} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			b.WriteString(rel + ": absent\n")
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > maxArchetypeDirectoryEntries {
			return "", false
		}
		b.WriteString(rel + ":\n")
		for _, entry := range entries {
			entryInfo, err := entry.Info()
			if err != nil {
				return "", false
			}
			b.WriteString(entry.Name() + " " + entryInfo.Mode().String() + " " + strconv.FormatInt(entryInfo.Size(), 10) + " " +
				strconv.FormatInt(entryInfo.ModTime().UnixNano(), 10) + "\n")
		}
	}
	return b.String(), true
}
