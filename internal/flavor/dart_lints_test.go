package flavor_test

// BUG-1010: mobile-flutter detects any pubspec.yaml, and its analysis_options.yaml included
// package:lints/recommended.yaml whether or not the repository depends on lints. Where it does
// not, flutter analyze reports the include as missing and the required test job fails. The
// scaffolded config now includes the rule set of a lint package pubspec.yaml declares, and no
// include otherwise.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"gopkg.in/yaml.v3"
)

// dartAnalysisConfig is the part of analysis_options.yaml the check reads.
type dartAnalysisConfig struct {
	Include string `yaml:"include"`
	Linter  struct {
		Rules []string `yaml:"rules"`
	} `yaml:"linter"`
}

func TestScaffoldedDartAnalysisConfigIncludesOnlyADeclaredLintPackage(t *testing.T) {
	const recommended = "package:lints/recommended.yaml"
	const flutterSet = "package:flutter_lints/flutter.yaml"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		include string
	}{
		// Positive: a declared lint package's rule set is included, from either section.
		{"lints-dev-dependency", map[string]string{"pubspec.yaml": "name: app\ndev_dependencies:\n  lints: ^6.0.0\n"}, recommended},
		{"lints-dependency", map[string]string{"pubspec.yaml": "name: app\ndependencies:\n  lints: ^6.0.0\n"}, recommended},
		{"flutter-lints", map[string]string{"pubspec.yaml": "name: app\ndev_dependencies:\n  flutter_lints: ^6.0.0\n"}, flutterSet},
		// Boundary: flutter_lints' set already includes lints', so it wins when both are declared.
		{"both-declared", map[string]string{"pubspec.yaml": "name: app\ndev_dependencies:\n  lints: ^6.0.0\n  flutter_lints: ^6.0.0\n"}, flutterSet},
		// Negative: the row's case, a pubspec without either package, gets no include.
		{"no-lint-package", map[string]string{"pubspec.yaml": "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n"}, ""},
		// Boundary: pub matches keys exactly, and an override is not a dependency.
		{"case-folded-key", map[string]string{"pubspec.yaml": "name: app\ndev_dependencies:\n  Lints: ^6.0.0\n"}, ""},
		{"override-only", map[string]string{"pubspec.yaml": "name: app\ndependency_overrides:\n  lints: ^6.0.0\n"}, ""},
		// Boundary: a pubspec that does not parse, or none at all, declares nothing to rely on.
		{"unparseable-pubspec", map[string]string{"pubspec.yaml": "dev_dependencies: [lints\n"}, ""},
		{"no-pubspec", map[string]string{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := repoWithFiles(t, tc.files)
			report, err := flavor.ApplyFlavor(t.Context(), repo, "mobile-flutter", false)
			if err != nil || len(report.UnmetTemplates) > 0 {
				t.Fatalf("apply: err %v, unmet %v", err, report.UnmetTemplates)
			}
			data, err := os.ReadFile(filepath.Join(repo, "analysis_options.yaml"))
			if err != nil {
				t.Fatalf("analysis_options.yaml was not scaffolded: %v", err)
			}
			var config dartAnalysisConfig
			if err := yaml.Unmarshal(data, &config); err != nil {
				t.Fatalf("parse analysis_options.yaml: %v\n%s", err, data)
			}
			if config.Include != tc.include {
				t.Errorf("include = %q, want %q:\n%s", config.Include, tc.include, data)
			}
			if !slices.Contains(config.Linter.Rules, "unawaited_futures") {
				t.Errorf("the core linter rules are gone:\n%s", data)
			}
		})
	}
}
