// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// prettierTabSources are the declarative Prettier configurations prettierTabWarning reads, in
// Prettier's own search order; package.json holds its configuration under the "prettier" key.
// A JavaScript configuration is code, so adoption does not evaluate it and names nothing for it.
var prettierTabSources = []string{
	"package.json",
	".prettierrc",
	".prettierrc.json",
	".prettierrc.yaml",
	".prettierrc.yml",
}

// maxPrettierOverrides bounds the overrides read from one configuration.
const maxPrettierOverrides = 256

// prettierTabWarning names the override a repository needs when its Prettier configuration
// indents with tabs (#1246). Prettier formats code embedded in a Markdown file with that file's
// options, so useTabs: true puts a tab in every indented line of every code sample, and the
// documentation gate refuses those tabs (MD010) outside the fence languages that need them. The
// warning is empty when no declarative configuration sets useTabs: true, when one already turns
// it off for Markdown in an override, or when the configuration does not parse: Prettier reports
// that itself.
func prettierTabWarning(ctx context.Context, repoPath string) (string, error) {
	for index := 0; index < len(prettierTabSources); index++ {
		name := prettierTabSources[index]
		full, err := repoFile(repoPath, name)
		if err != nil {
			return "", err
		}
		data, exists, err := contextopt.ObserveSnapshot(ctx, full)
		if err != nil {
			return "", fmt.Errorf("inspect %s for Prettier's tab setting: %w", name, err)
		}
		if !exists {
			continue
		}
		config, found := prettierConfiguration(name, data)
		if !found {
			continue
		}
		if config["useTabs"] != true || markdownOverridesTabs(config["overrides"]) {
			return "", nil
		}
		return fmt.Sprintf("documentation gate: %s sets Prettier's useTabs: true, so every indented line of a "+
			"code sample in Markdown gets a tab and the gate refuses it (MD010) outside makefile, make, go, tsv "+
			"and diff fences; add a Prettier override for *.md and *.mdx files with useTabs: false", name), nil
	}
	return "", nil
}

// prettierConfiguration returns the Prettier configuration mapping of one source and whether the
// source holds one. YAML reads the JSON forms too.
func prettierConfiguration(name string, data []byte) (map[string]any, bool) {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil || document == nil {
		return nil, false
	}
	if name != "package.json" {
		return document, true
	}
	config, ok := document["prettier"].(map[string]any)
	return config, ok
}

// markdownOverridesTabs reports whether an override turns useTabs off for Markdown files.
func markdownOverridesTabs(value any) bool {
	overrides, _ := value.([]any)
	for index := 0; index < len(overrides) && index < maxPrettierOverrides; index++ {
		override, _ := overrides[index].(map[string]any)
		options, _ := override["options"].(map[string]any)
		if options["useTabs"] == false && overrideNamesMarkdown(override["files"]) {
			return true
		}
	}
	return false
}

// overrideNamesMarkdown reports whether an override's files pattern, a string or a list of
// strings, names Markdown files.
func overrideNamesMarkdown(files any) bool {
	patterns, ok := files.([]any)
	if !ok {
		patterns = []any{files}
	}
	for index := 0; index < len(patterns) && index < maxPrettierOverrides; index++ {
		pattern, _ := patterns[index].(string)
		if strings.Contains(pattern, ".md") || strings.Contains(pattern, "{md") || strings.Contains(pattern, ",md") {
			return true
		}
	}
	return false
}
