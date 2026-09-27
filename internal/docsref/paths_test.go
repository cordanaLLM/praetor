// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import "testing"

var testTops = map[string]bool{"internal": true, ".config": true, "docs": true, "deploy": true}

func TestPathReferenceOf_Positive_RepositoryShapes(t *testing.T) {
	cases := []struct {
		token string
		want  pathReference
	}{
		{"internal/forge/pr.go", pathReference{path: "internal/forge/pr.go", text: "internal/forge/pr.go"}},
		{"./internal/forge/", pathReference{path: "internal/forge", dir: true, text: "internal/forge/"}},
		{".config/models/routing.yaml:12-40,55", pathReference{path: ".config/models/routing.yaml", text: ".config/models/routing.yaml"}},
		{"--config=.config/x.json", pathReference{path: ".config/x.json", text: ".config/x.json"}},
		{"internal/state.VerifyStateSync", pathReference{path: "internal/state", dir: true, symbol: "VerifyStateSync", text: "internal/state.VerifyStateSync"}},
		{"(docs/guides/a.md#section),", pathReference{path: "docs/guides/a.md", text: "docs/guides/a.md"}},
	}
	for _, tc := range cases {
		got, ok := pathReferenceOf(tc.token, testTops)
		if !ok || got != tc.want {
			t.Errorf("pathReferenceOf(%q) = %+v, %v; want %+v", tc.token, got, ok, tc.want)
		}
	}
}

func TestPathReferenceOf_Negative_NotRepositoryPaths(t *testing.T) {
	for _, token := range []string{
		"https://example.com/internal/x.go", "/etc/internal/x", "~/internal/x", "$HOME/internal",
		"README.md", "lusoris/praetor", "docker/login-action@v4", "../internal/x.go", "internal",
	} {
		if got, ok := pathReferenceOf(token, testTops); ok {
			t.Errorf("pathReferenceOf(%q) = %+v, want no path", token, got)
		}
	}
}

func TestPathReferenceOf_Boundary_GlobsAndPlaceholdersKeepTheirPrefix(t *testing.T) {
	cases := map[string]pathReference{
		"internal/*/events.go":        {path: "internal", dir: true, text: "internal/*/events.go"},
		"docs/adr/<NNNN>-title.md":    {path: "docs/adr", dir: true, text: "docs/adr/<NNNN>-title.md"},
		".config/orgs/{a,b}.yaml":     {path: ".config/orgs", dir: true, text: ".config/orgs/{a,b}.yaml"},
		"deploy/arc/scale-set.yaml.":  {path: "deploy/arc/scale-set.yaml", text: "deploy/arc/scale-set.yaml"},
		"internal/FILE/PATH.go/extra": {path: "internal", dir: true, text: "internal/FILE/PATH.go/extra"},
	}
	for token, want := range cases {
		got, ok := pathReferenceOf(token, testTops)
		if !ok || got != want {
			t.Errorf("pathReferenceOf(%q) = %+v, %v; want %+v", token, got, ok, want)
		}
	}
	if got, ok := pathReferenceOf("*/x.go", testTops); ok {
		t.Errorf("a glob in the first element names no repository path, got %+v", got)
	}
}
