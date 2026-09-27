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
		{"internal/state.VerifyStateSync", pathReference{path: "internal/state.VerifyStateSync", text: "internal/state.VerifyStateSync"}},
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

// symbolFixture is a module with one package whose identifiers the path check resolves.
func symbolFixture(t *testing.T) (*repositoryIndex, *sourceTree) {
	t.Helper()
	root, inventory := writeModule(t, map[string]string{
		"internal/pkg/pkg.go": "package pkg\n\ntype Registry struct{}\n\nfunc (Registry) Lookup() {}\n\n" +
			"func Exported() {}\n\nfunc unexported() {}\n",
		"internal/pkg/pr.go": "package pkg\n",
	})
	index := &repositoryIndex{files: map[string]bool{}, dirs: map[string]bool{}, tops: map[string]bool{}}
	for _, rel := range inventory {
		index.add(rel)
	}
	tree, err := newSourceTree(root, inventory)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	return index, tree
}

// resolve runs the path check on one written reference.
func resolve(t *testing.T, index *repositoryIndex, tree *sourceTree, text string) (string, bool) {
	t.Helper()
	ref, ok := pathReferenceOf(text, index.tops)
	if !ok {
		t.Fatalf("pathReferenceOf(%q) found no path", text)
	}
	return index.pathProblem(t.Context(), tree, ref)
}

func TestPathProblem_Positive_FilesPackagesAndIdentifiers(t *testing.T) {
	index, tree := symbolFixture(t)
	for _, text := range []string{
		"internal/pkg/pr.go", "internal/pkg", "internal/pkg/", "internal/pkg.Exported",
		"internal/pkg.unexported", "internal/pkg.Registry", "internal/pkg.Lookup", "internal/pkg.Registry.Lookup",
	} {
		if problem, absent := resolve(t, index, tree, text); problem != "" || absent {
			t.Errorf("%q: problem %q absent %v, want it to resolve", text, problem, absent)
		}
	}
}

func TestPathProblem_Negative_MissingIdentifierAndPath(t *testing.T) {
	index, tree := symbolFixture(t)
	if problem, absent := resolve(t, index, tree, "internal/pkg.missing"); absent || problem != `package internal/pkg declares no missing (in "internal/pkg.missing")` {
		t.Errorf("missing identifier: problem %q absent %v", problem, absent)
	}
	for _, text := range []string{"internal/pkg/gone.go", "internal/other.Exported", "internal/pkg/pr.go/"} {
		if _, absent := resolve(t, index, tree, text); !absent {
			t.Errorf("%q must be reported absent", text)
		}
	}
}
