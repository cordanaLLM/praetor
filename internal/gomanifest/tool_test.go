// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gomanifest

import "testing"

func TestDeclaresTool_Positive_RequirementOrToolDirective(t *testing.T) {
	for name, manifest := range map[string]string{
		"requirement": "require golang.org/x/vuln v1.8.0\n",
		"tool":        "tool golang.org/x/vuln/cmd/govulncheck\n",
		"tool block":  "tool (\n\tgolang.org/x/vuln/cmd/govulncheck\n)\n",
	} {
		if !DeclaresTool([]byte(manifest), "golang.org/x/vuln", "golang.org/x/vuln/cmd/govulncheck") {
			t.Errorf("%s: tool not found", name)
		}
	}
}

func TestDeclaresTool_Negative_CommentsAndOtherModules(t *testing.T) {
	manifest := "// require golang.org/x/vuln v1.8.0\nrequire example.com/other v1.0.0\n// tool golang.org/x/vuln/cmd/govulncheck\n"
	if DeclaresTool([]byte(manifest), "golang.org/x/vuln", "golang.org/x/vuln/cmd/govulncheck") {
		t.Fatal("comments or another requirement declared govulncheck")
	}
}

func TestDeclaresTool_Boundary_BOMAndTrailingComment(t *testing.T) {
	manifest := "\ufeffmodule example.com/tools\n\ntool golang.org/x/vuln/cmd/govulncheck // pinned\n"
	if !DeclaresTool([]byte(manifest), "golang.org/x/vuln", "golang.org/x/vuln/cmd/govulncheck") {
		t.Fatal("BOM or trailing comment hid the tool directive")
	}
}
