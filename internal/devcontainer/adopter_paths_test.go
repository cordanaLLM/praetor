package devcontainer

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// renderedBundle renders a prepared bundle's devcontainer.json for profiles.
func renderedBundle(t *testing.T, profiles []string, options BootstrapOptions) string {
	t.Helper()
	bundle, err := PrepareBundle(t.Context(), "adopted/app", profiles, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Render(bundle.Config)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A generated bundle is written into the adopter's repository, so it must not name a
// path that exists only in Praetor's own tree or in one native repository's layout.
func TestGeneratedBundleNamesNoPraetorOrFixedNativePaths(t *testing.T) {
	cases := map[string]struct {
		profiles []string
		options  BootstrapOptions
	}{
		"native-unavailable":    {[]string{NativeGPUProfile}, BootstrapOptions{}},
		"native-ready":          {[]string{NativeGPUProfile}, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)}},
		"framework-ready":       {[]string{"framework"}, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)}},
		"framework-and-native":  {[]string{"framework", NativeGPUProfile}, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)}},
		"framework-unavailable": {[]string{"framework"}, BootstrapOptions{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rendered := renderedBundle(t, tc.profiles, tc.options)
			for _, forbidden := range []string{"./cmd/standardsctl", "go run", "core/build", "--compile-commands-dir", DefaultDockerfilePath} {
				if strings.Contains(rendered, forbidden) {
					t.Fatalf("generated bundle names adopter-foreign %q:\n%s", forbidden, rendered)
				}
			}
		})
	}
}

func TestNativeClangdArgumentsAreTheSharedSet(t *testing.T) {
	dc, err := SynthesizeFromProfiles("adopted/app", []string{NativeGPUProfile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dc.Customizations.VSCode.Settings["clangd.arguments"].([]string)
	if !ok || !slices.Equal(got, util.ClangdArguments()) {
		t.Fatalf("native clangd arguments diverge from util.ClangdArguments: %#v", dc.Customizations.VSCode.Settings["clangd.arguments"])
	}
	framework, err := SynthesizeFromProfiles("adopted/app", []string{"framework"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := framework.Customizations.VSCode.Settings["clangd.arguments"]; present {
		t.Fatal("framework container received clangd arguments")
	}
}
