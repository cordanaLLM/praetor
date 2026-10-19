// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package figures

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func findNodeModulesPath(t *testing.T) string {
	t.Helper()
	// PRAETOR_FIGURES_NODE_MODULES names a node_modules folder outside the checkout that holds eslint.
	candidates := []string{filepath.Join("..", "..", "node_modules")}
	if dir := os.Getenv("PRAETOR_FIGURES_NODE_MODULES"); dir != "" {
		candidates = append([]string{dir}, candidates...)
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

const eslintTestScript = `
const { ESLint } = require('eslint');
const tsPlugin = require('@typescript-eslint/eslint-plugin');
const tsParser = require('@typescript-eslint/parser');

async function main() {
  const mode = process.argv[1];
  if (mode === 'typed-only') {
    const eslint = new ESLint({
      overrideConfigFile: true,
      overrideConfig: [{
        files: ['**/*.js'],
        plugins: { '@typescript-eslint': tsPlugin },
        languageOptions: {
          parser: tsParser,
          parserOptions: { project: true },
        },
      }],
    });
    const results = await eslint.lintFiles(['tools/figures/player/player.js']);
    if (results[0].errorCount === 0) {
      console.error('typed-only unexpectedly passed');
      process.exit(1);
    }
    console.log('typed-only correctly failed with errors:', results[0].errorCount);
    return;
  }

  const figures = (await import('./tools/figures/eslint.figures.mjs')).default;
  const figuresEntry = figures(tsPlugin);

  if (mode === 'with-figures-entry') {
    const eslint = new ESLint({
      overrideConfigFile: true,
      overrideConfig: [
        {
          files: ['**/*.js'],
          plugins: { '@typescript-eslint': tsPlugin },
          languageOptions: {
            parser: tsParser,
            parserOptions: { project: true },
          },
        },
        figuresEntry,
      ],
    });
    const results = await eslint.lintFiles(['tools/figures/player/player.js', 'tools/figures/player/loader.js']);
    const totalErrors = results.reduce((acc, r) => acc + r.errorCount, 0);
    if (totalErrors !== 0) {
      console.error('with-figures-entry failed with errors:', totalErrors, results);
      process.exit(1);
    }
    console.log('with-figures-entry passed cleanly');
    return;
  }

  if (mode === 'planted-error') {
    const eslint = new ESLint({
      overrideConfigFile: true,
      overrideConfig: [
        {
          files: ['**/*.js'],
          plugins: { '@typescript-eslint': tsPlugin },
          languageOptions: {
            parser: tsParser,
            parserOptions: { project: true },
          },
          rules: {
            'no-undef': 'error',
          },
        },
        figuresEntry,
      ],
    });
    const results = await eslint.lintText('plantedUndefinedCall();', { filePath: 'tools/figures/player/loader.js' });
    if (results[0].errorCount === 0) {
      console.error('planted-error unexpectedly passed');
      process.exit(1);
    }
    const hasNoUndef = results[0].messages.some(m => m.ruleId === 'no-undef');
    if (!hasNoUndef) {
      console.error('planted-error reported wrong rules:', results[0].messages);
      process.exit(1);
    }
    console.log('planted-error correctly caught no-undef');
    return;
  }

  console.error('unknown mode:', mode);
  process.exit(2);
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
`

func runESLintSubtest(t *testing.T, nodeBin, repoRoot, nodeModules, mode, wantSubstr string) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), append(os.Environ(), "NODE_PATH="+nodeModules))
	if err != nil {
		t.Fatal(err)
	}
	res, err := util.RunCommandBytes(ctx, repoRoot, nodeBin, util.MaxCommandOutputBytes, "-e", eslintTestScript, mode)
	out := append(res.Stdout, res.Stderr...)
	if err != nil {
		t.Fatalf("mode %s failed (%v):\n%s", mode, err, string(out))
	}
	if !strings.Contains(string(out), wantSubstr) {
		t.Fatalf("mode %s output %q missing want %q", mode, string(out), wantSubstr)
	}
}

func TestESLint_FiguresConfiguration(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable not found")
	}
	nodeModules := findNodeModulesPath(t)
	if nodeModules == "" {
		t.Skip("node_modules with eslint not found")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	modes := []struct {
		name       string
		mode       string
		wantSubstr string
	}{
		{name: "TypedOnlyFails", mode: "typed-only", wantSubstr: "typed-only correctly failed"},
		{name: "WithFiguresEntryPasses", mode: "with-figures-entry", wantSubstr: "with-figures-entry passed cleanly"},
		{name: "PlantedErrorCaught", mode: "planted-error", wantSubstr: "planted-error correctly caught no-undef"},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			runESLintSubtest(t, nodeBin, repoRoot, nodeModules, tc.mode, tc.wantSubstr)
		})
	}
}
