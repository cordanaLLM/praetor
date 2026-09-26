package devcontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/templates"
)

// templateEmbedSource is templates/embed.go reduced to what the capture reads: its directive.
func templateEmbedSource(directive string) string {
	return "package templates\n\nimport \"embed\"\n\n" + directive + "\nvar shipped embed.FS\n"
}

// writeTemplateFamily writes the template embedding source and every template body it
// embeds into a bootstrap fixture.
func writeTemplateFamily(t *testing.T, root, directive string) []string {
	t.Helper()
	writeBootstrapFile(t, root, templates.SourceFile, templateEmbedSource(directive))
	assets, err := templateBootstrapAssetPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) == 0 {
		t.Fatal("no embedded templates declared; the capture would be checked against nothing")
	}
	for _, asset := range assets {
		writeBootstrapFile(t, root, asset, "fixture\n")
	}
	return assets
}

// Positive: templates/embed.go embeds the flavor bodies, and go build ./cmd/standardsctl
// imports it through internal/flavor, so the archive must carry every body or the
// bootstrapped binary does not build.
func TestBootstrapCapturesTheEmbeddedTemplates(t *testing.T) {
	root := bootstrapSourceFixture(t)
	assets := writeTemplateFamily(t, root, "//go:embed "+templates.Pattern)
	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if !containsBootstrapSource(files, asset) {
			t.Fatalf("embedded template %s was omitted from the capture", asset)
		}
	}
}

// Negative: a missing body fails the capture and names the file, and a directive other than
// the declared one is refused, since the archive carries only the declared assets.
func TestBootstrapRefusesAnIncompleteOrUndeclaredTemplateEmbed(t *testing.T) {
	root := bootstrapSourceFixture(t)
	assets := writeTemplateFamily(t, root, "//go:embed "+templates.Pattern)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(assets[0]))); err != nil {
		t.Fatal(err)
	}
	if _, err := captureBootstrapSource(t.Context(), root); err == nil || !strings.Contains(err.Error(), assets[0]) {
		t.Fatalf("missing embedded template accepted: %v", err)
	}

	widened := bootstrapSourceFixture(t)
	writeTemplateFamily(t, widened, "//go:embed all:go")
	if _, err := captureBootstrapSource(t.Context(), widened); err == nil || !strings.Contains(err.Error(), "explicit asset capture is required") {
		t.Fatalf("an undeclared template directive was accepted: %v", err)
	}
}

// Boundary: a template body is admitted by name only; a stray file beside the bodies that no
// directive embeds is not, and neither is a template path on Go's test surface.
func TestBootstrapTemplateAllowanceIsExact(t *testing.T) {
	assets, err := templateBootstrapAssetPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if err := validateBootstrapSourceName(asset); err != nil {
			t.Errorf("declared template %s refused: %v", asset, err)
		}
	}
	for _, stray := range []string{templates.Directory + "/go/README.md", templates.Directory + "/go/extra.tmpl", templates.Directory + "/testdata/x.tmpl"} {
		if err := validateBootstrapSourceName(stray); err == nil {
			t.Errorf("validateBootstrapSourceName(%q) admitted a file no directive embeds", stray)
		}
	}
}
