package flavor

import (
	"context"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// dartLintPackages are the lint packages whose rule set the scaffolded Dart analyzer config
// (templates/flutter/analysis_options.yaml.tmpl) can include, preferred first: flutter_lints'
// flutter.yaml includes lints' recommended set and adds Flutter's own rules.
var dartLintPackages = []string{"flutter_lints", "lints"}

// pubspecDependencies is what the analyzer config reads from pubspec.yaml. yaml.v3 matches keys
// exactly, as pub does.
type pubspecDependencies struct {
	Dependencies    map[string]yaml.Node `yaml:"dependencies"`
	DevDependencies map[string]yaml.Node `yaml:"dev_dependencies"`
}

// dartAnalysisFacts resolves the lint package the scaffolded analysis_options.yaml includes.
//
// mobile-flutter detects any pubspec.yaml, and an include such as
// package:lints/recommended.yaml resolves only where pub fetched that package: elsewhere
// flutter analyze reports the include as missing and the required test job fails. The config
// therefore includes the rule set of a lint package pubspec.yaml declares, as a dependency or a
// dev dependency, and none otherwise; its own rules are core linter rules, which need no
// package. A pubspec.yaml that cannot be read or parsed declares nothing to rely on, so it gets
// the config without an include. The file is never withheld.
func dartAnalysisFacts(_ context.Context, repoPath string) (templates.Context, string) {
	return templates.Context{DartLints: declaredDartLints(repoPath)}, ""
}

// declaredDartLints returns the preferred lint package pubspec.yaml declares, or "".
func declaredDartLints(repoPath string) string {
	data, err := util.ReadConfinedLimited(repoPath, "pubspec.yaml", maxSettingBytes)
	if err != nil {
		return ""
	}
	var pubspec pubspecDependencies
	if err := yaml.Unmarshal(data, &pubspec); err != nil {
		return ""
	}
	for _, name := range dartLintPackages {
		_, dependency := pubspec.Dependencies[name]
		_, devDependency := pubspec.DevDependencies[name]
		if dependency || devDependency {
			return name
		}
	}
	return ""
}
