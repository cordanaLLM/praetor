// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The bootstrap's Markdown asset family was moved onto the managed asset family registry as
// a pure refactor. This golden was recorded before that change and pins the declared families
// (the template family's asset list is left out: it grows with every shipped template) and
// the verdicts of the asset and directive rules on the Markdown gate's paths. The figure engine
// family added its declaration and turned tools/figures/build.mjs into an admitted asset, while
// its repository-only files, such as bundle.mjs and the npm lock, stay refused.
func TestBootstrapAssetFamiliesGolden(t *testing.T) {
	families, err := bootstrapAssetFamilies()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, family := range families {
		fmt.Fprintf(&sb, "family %q source %q directive %q\n", family.name, family.source, family.directive)
		if strings.HasPrefix(family.source, "tools/") {
			fmt.Fprintf(&sb, "  assets %q\n", family.assets)
		}
	}
	for _, name := range []string{
		"tools/markdownlint/verify.mjs", "tools/markdownlint/package-lock.json", "tools/markdownlint/README.md",
		"tools/markdownlint/assets.go", "tools/figures/build.mjs", "tools/figures/bundle.mjs",
		"tools/figures/package-lock.json", "templates/go/ci-go.yml.tmpl",
	} {
		asset, assetErr := isBootstrapAsset(name)
		fmt.Fprintf(&sb, "asset %s %t %v\n", name, asset, assetErr)
		fmt.Fprintf(&sb, "name %s %v\n", name, validateBootstrapSourceName(name))
	}
	markdownDirective := "//go:embed package.json package-lock.json markdownlint-cli2.yaml verify.mjs no-private-scratch-links.mjs"
	for _, probe := range []struct{ source, text string }{
		{"tools/markdownlint/assets.go", markdownDirective},
		{"tools/markdownlint/assets.go", "//go:embed verify.mjs"},
		{"tools/markdownlint/other.go", markdownDirective},
		{"tools/markdownlint/assets.go", "// go:embed is only prose"},
	} {
		fmt.Fprintf(&sb, "directive %s %q %v\n", probe.source, probe.text, validateEmbedDirective(probe.source, probe.text))
	}
	testsupport.AssertGolden(t, filepath.Join("testdata", "managed-family", "bootstrap-families.golden"), sb.String())
}
