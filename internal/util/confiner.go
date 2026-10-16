// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrConfinementRootChanged reports a Confiner whose root is no longer the directory it resolved:
// it was removed, renamed away, or replaced, by a link among others, after the Confiner was built.
var ErrConfinementRootChanged = errors.New("util: the confinement root changed after it was resolved")

// Confiner is ConfinePath for one root resolved once (#1382). ConfinePath resolved the root, the
// candidate's parent and the candidate through filepath.EvalSymlinks on every call, and on Windows
// each of those normalises every path component from the volume down: a third to a half of an
// adoption went there. A Confiner resolves the root when it is built and records which directory
// that is. Path then makes the lexical checks ConfinePath makes, confirms with one Lstat that the
// root is still that directory, and Lstats only the components below the root: when none is a
// link (a symbolic link, or a junction or other reparse point, which Lstat reports as irregular),
// the candidate lies in the resolved root as written. A component that is a link takes
// ConfinePath's full resolution against the resolved root instead, so a link to outside the root
// is refused exactly as before. The window between the check and the caller's own open is the
// one ConfinePath documents; writers that must close it still use WriteFileConfined.
type Confiner struct {
	absRoot      string
	resolvedRoot string
	rootInfo     os.FileInfo
}

// NewConfiner resolves root for a Confiner. A root that does not exist yet has no fast path: Path
// then resolves every call as ConfinePath does.
func NewConfiner(root string) (*Confiner, error) {
	absRoot, _, err := lexicalConfine(root, ".")
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := resolveExistingAncestor(absRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absRoot)
	if errors.Is(err, os.ErrNotExist) {
		return &Confiner{absRoot: absRoot, resolvedRoot: resolvedRoot}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("util: inspect the confinement root %q: %w", absRoot, err)
	}
	return &Confiner{absRoot: absRoot, resolvedRoot: resolvedRoot, rootInfo: info}, nil
}

// Root is the confiner's absolute root, unresolved.
func (c *Confiner) Root() string { return c.absRoot }

// Path is ConfinePath(root, rel) for the confiner's root: the cleaned lexical join of rel below
// the root, refused when it leaves the root lexically or through a link, and refused with
// ErrConfinementRootChanged when the root is no longer the directory the Confiner resolved.
func (c *Confiner) Path(rel string) (string, error) {
	_, candidate, err := lexicalConfine(c.absRoot, rel)
	if err != nil {
		return "", err
	}
	if c.rootInfo == nil {
		_, candidate, err = confineResolved(c.absRoot, candidate)
		return candidate, err
	}
	now, err := os.Lstat(c.absRoot)
	if err != nil || !os.SameFile(now, c.rootInfo) || now.Mode().Type() != c.rootInfo.Mode().Type() {
		return "", fmt.Errorf("%w: %q", ErrConfinementRootChanged, c.absRoot)
	}
	if candidate == c.absRoot {
		return candidate, nil
	}
	inside, err := filepath.Rel(c.absRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("util: relate %q to the confinement root %q: %w", candidate, c.absRoot, err)
	}
	current := c.absRoot
	for _, element := range strings.Split(inside, string(filepath.Separator)) {
		current = filepath.Join(current, element)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("util: inspect %q: %w", current, err)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			if err := checkResolvedConfinement(candidate, c.resolvedRoot); err != nil {
				return "", err
			}
			return candidate, nil
		}
	}
	return candidate, nil
}

// scopedConfiners are the Confiners ScopeConfiner registered, by absolute root.
var scopedConfiners = struct {
	sync.Mutex
	byRoot map[string]*scopedConfiner
}{byRoot: map[string]*scopedConfiner{}}

// scopedConfiner is one registered Confiner and how many scopes hold it.
type scopedConfiner struct {
	confiner *Confiner
	refs     int
}

// ScopeConfiner makes every ConfinePath call for root, by its absolute form, go through one
// Confiner until release is called, so one run resolves the root once however many paths it
// confines. Scopes of one root nest: the first builds the Confiner, the last release drops it.
// A run that ends releases it, so a long-lived process never carries a resolved root from one
// run to the next.
func ScopeConfiner(root string) (release func(), err error) {
	absRoot, _, err := lexicalConfine(root, ".")
	if err != nil {
		return nil, err
	}
	scopedConfiners.Lock()
	defer scopedConfiners.Unlock()
	entry, held := scopedConfiners.byRoot[absRoot]
	if !held {
		confiner, err := NewConfiner(absRoot)
		if err != nil {
			return nil, err
		}
		entry = &scopedConfiner{confiner: confiner}
		scopedConfiners.byRoot[absRoot] = entry
	}
	entry.refs++
	var once sync.Once
	return func() {
		once.Do(func() {
			scopedConfiners.Lock()
			defer scopedConfiners.Unlock()
			if entry.refs--; entry.refs == 0 && scopedConfiners.byRoot[absRoot] == entry {
				delete(scopedConfiners.byRoot, absRoot)
			}
		})
	}, nil
}

// scopedConfinerFor returns the Confiner a scope holds for absRoot, or nil.
func scopedConfinerFor(absRoot string) *Confiner {
	scopedConfiners.Lock()
	defer scopedConfiners.Unlock()
	if entry, held := scopedConfiners.byRoot[absRoot]; held {
		return entry.confiner
	}
	return nil
}
