// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package operationalsync

import "testing"

func TestIsOwnerOnlyReference_Positive_PrefixesAndPathsUnderThem(t *testing.T) {
	for _, rel := range []string{
		"deploy/arc/", "deploy/arc", "deploy/k8s/app/kustomization.yaml", ".config/operator/",
		".config/operator/funding.yaml", ".config/fleet-topology.yaml", ".config/fleet.yaml",
	} {
		if !IsOwnerOnlyReference(rel) {
			t.Errorf("IsOwnerOnlyReference(%q) = false, want true", rel)
		}
	}
}

func TestIsOwnerOnlyReference_Negative_EnginePathsAndNeighbours(t *testing.T) {
	for _, rel := range []string{
		"deploy/helm/praetor/Chart.yaml", "deploy/k8sx/a.yaml", ".config/orgs.yaml", "deploy", ".config/",
	} {
		if IsOwnerOnlyReference(rel) {
			t.Errorf("IsOwnerOnlyReference(%q) = true, want false", rel)
		}
	}
}

func TestIsOwnerOnlyReference_Boundary_ExactFilesMatchWholeNames(t *testing.T) {
	cases := map[string]bool{
		".config/fleet.yaml":      true,
		".config/fleet.yaml/":     true,
		".config/fleet.yaml.bak":  false,
		".config/orgs/":           true,
		".config/orgs/deep/a.yml": true,
		".config/orgsx/a.yaml":    false,
		"":                        false,
	}
	for rel, want := range cases {
		if got := IsOwnerOnlyReference(rel); got != want {
			t.Errorf("IsOwnerOnlyReference(%q) = %v, want %v", rel, got, want)
		}
	}
}
