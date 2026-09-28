package config

import (
	"fmt"
	"strings"
	"testing"
)

func loadDocumentationManifest(t *testing.T, section string) (*Manifest, error) {
	t.Helper()
	return LoadManifest(writeManifest(t, "version: 1\n"+section))
}

func styleExcludeSection(globs ...string) string {
	var section strings.Builder
	section.WriteString("documentation:\n  style_exclude:\n")
	for _, glob := range globs {
		fmt.Fprintf(&section, "    - %q\n", glob)
	}
	return section.String()
}

// Positive: an absent block keeps the gate's defaults, and a declared block is taken as written
// and survives RenderManifest, which adoption uses to rewrite the manifest.
func TestLoadManifestDocumentationPositive(t *testing.T) {
	m, err := loadDocumentationManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Documentation != nil || m.Documentation.EffectiveMaxFiles() != DefaultDocumentationMaxFiles ||
		m.Documentation.EffectiveMaxFileBytes() != DefaultDocumentationMaxFileBytes {
		t.Fatalf("absent documentation block = %+v, want the defaults", m.Documentation)
	}
	m, err = loadDocumentationManifest(t, `documentation:
  max_files: 8192
  style_exclude:
    - "changelog.d/**"
    - "docs/adr/_index_fragments/*.md"
`)
	if err != nil {
		t.Fatal(err)
	}
	policy := m.Documentation
	if policy.EffectiveMaxFiles() != 8192 || policy.EffectiveMaxFileBytes() != DefaultDocumentationMaxFileBytes ||
		strings.Join(policy.StyleExclude, ",") != "changelog.d/**,docs/adr/_index_fragments/*.md" {
		t.Fatalf("declared documentation block = %+v", policy)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil {
		t.Fatalf("rendered manifest does not reload: %v\n%s", err, rendered)
	}
	if again.Documentation.EffectiveMaxFiles() != 8192 || again.Documentation.MaxFileBytes != nil ||
		len(again.Documentation.StyleExclude) != 2 {
		t.Fatalf("documentation block lost in the render round trip: %+v", again.Documentation)
	}
}

// Negative: a bound outside its range, a bound or glob of the wrong YAML type, an unknown or
// repeated key, and every refused glob shape fail LoadManifest and name the problem.
func TestLoadManifestDocumentationNegative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"files over ceiling":     {"documentation: {max_files: 16385}\n", "documentation.max_files must be an integer from 4096 to 16384; got 16385"},
		"files under default":    {"documentation: {max_files: 4095}\n", "got 4095"},
		"bytes over ceiling":     {"documentation: {max_file_bytes: 4194305}\n", "documentation.max_file_bytes must be an integer from 1048576 to 4194304; got 4194305"},
		"bytes under default":    {"documentation: {max_file_bytes: 1048575}\n", "got 1048575"},
		"quoted bound":           {"documentation: {max_files: \"8192\"}\n", "documentation max_files must be an integer"},
		"fractional bound":       {"documentation: {max_files: 8192.5}\n", "documentation max_files must be an integer"},
		"null bound":             {"documentation: {max_files: null}\n", "documentation max_files must be an integer"},
		"unknown key":            {"documentation: {max_total_bytes: 1}\n", `unknown or duplicated documentation field "max_total_bytes"`},
		"repeated key":           {"documentation: {max_files: 8192, max_files: 8192}\n", `"max_files"`},
		"list block":             {"documentation: [max_files]\n", "documentation must be a mapping"},
		"scalar glob list":       {"documentation: {style_exclude: \"docs/**\"}\n", "documentation.style_exclude must be a list of globs"},
		"null glob list":         {"documentation: {style_exclude: null}\n", "documentation.style_exclude must be a list of globs"},
		"integer glob":           {"documentation: {style_exclude: [7]}\n", "documentation.style_exclude[0] must be a string"},
		"repeated glob":          {styleExcludeSection("docs/**", "docs/**"), `documentation.style_exclude[1] repeats "docs/**"`},
		"too many globs":         {styleExcludeSection(numberedGlobs(MaxDocumentationStyleExclusions + 1)...), "has 65 globs; maximum is 64"},
		"glob over byte maximum": {styleExcludeSection(globOfBytes(MaxDocumentationStyleExclusionBytes + 1)), "documentation.style_exclude[0] exceeds 256 bytes"},
	}
	for _, glob := range []string{"", "**", "*", "**/*", "*/**", "?*.*", "/docs/**", "!docs/**", "C:/docs/**",
		"../docs/**", "docs/../x/**", "./docs/**", "docs/", "docs//x.md", `docs\x.md`} {
		cases["glob "+glob] = struct{ section, want string }{styleExcludeSection(glob), "documentation.style_exclude[0] "}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadDocumentationManifest(t, tc.section)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadManifest(%q) error = %v, want %q", tc.section, err, tc.want)
			}
		})
	}
}

// Boundary: both bounds at their default and exactly at their ceiling, 64 globs, and one glob of
// exactly 256 bytes load; an empty glob list and an empty block declare nothing past defaults.
func TestLoadManifestDocumentationBoundary(t *testing.T) {
	for _, bounds := range [][2]int{
		{DefaultDocumentationMaxFiles, DefaultDocumentationMaxFileBytes},
		{DocumentationMaxFilesCeiling, DocumentationMaxFileBytesCeiling},
	} {
		m, err := loadDocumentationManifest(t, fmt.Sprintf("documentation:\n  max_files: %d\n  max_file_bytes: %d\n", bounds[0], bounds[1]))
		if err != nil {
			t.Fatalf("bounds %v: %v", bounds, err)
		}
		if m.Documentation.EffectiveMaxFiles() != bounds[0] || m.Documentation.EffectiveMaxFileBytes() != bounds[1] {
			t.Fatalf("bounds %v loaded as %+v", bounds, m.Documentation)
		}
	}
	for _, section := range []string{
		styleExcludeSection(numberedGlobs(MaxDocumentationStyleExclusions)...),
		styleExcludeSection(globOfBytes(MaxDocumentationStyleExclusionBytes)),
		"documentation: {style_exclude: []}\n",
		"documentation: {}\n",
	} {
		if _, err := loadDocumentationManifest(t, section); err != nil {
			t.Fatalf("LoadManifest(%q): %v", section, err)
		}
	}
	if problem := StyleExclusionProblem("docs/\u00e9t\u00e9/**"); problem != "" {
		t.Fatalf("a non-ASCII letter names a path, got %q", problem)
	}
	if problem := StyleExclusionProblem("docs/**/*.md"); problem != "" {
		t.Fatalf("a directory glob is refused: %q", problem)
	}
}

func numberedGlobs(count int) []string {
	globs := make([]string, 0, count)
	for index := range count {
		globs = append(globs, fmt.Sprintf("docs/generated-%d/**", index))
	}
	return globs
}

func globOfBytes(size int) string {
	return "docs/" + strings.Repeat("a", size-len("docs/")-len("/**")) + "/**"
}
