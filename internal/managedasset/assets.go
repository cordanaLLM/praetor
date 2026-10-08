// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"fmt"
)

// MaxAssets is the bound on the files Assets returns across the registry (HISS-02).
const MaxAssets = MaxFamilies * 64

// Asset is one managed file as adoption writes it.
type Asset struct {
	// Path is the repository-relative, slash-separated path of the file.
	Path string
	// Data is the canonical content the family owns at Path.
	Data []byte
}

// Assets returns every file any registered family writes, with its canonical bytes, in
// registry order. It is the one enumeration of the managed files: the lint harness
// (scripts/test_emitted_hook_lint.py, through internal/managedasset/export) reads it instead of
// listing assets by hand, so a new family is linted the day it is registered.
func Assets() ([]Asset, error) {
	families := Families()
	var assets []Asset
	for index := 0; index < len(families) && index < MaxFamilies; index++ {
		for _, rel := range families[index].ManagedPaths() {
			data, owned, err := families[index].Canonical(rel)
			if err != nil {
				return nil, fmt.Errorf("read the managed asset %s of %s: %w", rel, families[index].Name, err)
			}
			if !owned {
				return nil, fmt.Errorf("%s lists %s as managed but does not own it", families[index].Name, rel)
			}
			assets = append(assets, Asset{Path: rel, Data: data})
			if len(assets) > MaxAssets {
				return nil, fmt.Errorf("the registry yields more than %d managed assets", MaxAssets)
			}
		}
	}
	return assets, nil
}

// DraftSkipAssets returns the opt-in draft skip rendering (Family.WithDraftShape) of the hosted
// workflow of every registered family that has one, at the repository-relative path adoption
// writes it to when hosted_gates.draft is skip. Assets lists the fail-closed rendering of that
// path only, so the lint harness reaches the skip text through this second enumeration too.
func DraftSkipAssets() ([]Asset, error) {
	families := Families()
	var assets []Asset
	for index := 0; index < len(families) && index < MaxFamilies; index++ {
		if families[index].WorkflowFile == "" {
			continue
		}
		skip, err := families[index].WithDraftShape(true)
		if err != nil {
			return nil, fmt.Errorf("render the draft skip of %s: %w", families[index].Name, err)
		}
		assets = append(assets, Asset{Path: skip.WorkflowFile, Data: []byte(skip.Workflow)})
	}
	return assets, nil
}
