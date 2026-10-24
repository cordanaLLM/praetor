// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"slices"
	"testing"
)

func TestPlantedReadmeGeneratedBaseline(t *testing.T) {
	const file = ".readme-baseline.json"
	set, err := Resolve(t.Context(), nil, memoryTree{file: "{}\n"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range set.Artefacts {
		if a.Name == NameBaseline {
			found = true
			if !a.Active || !slices.Contains(a.Files, file) {
				t.Fatalf("README baseline missing from generated artefact: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("debt baseline artefact missing")
	}
}
