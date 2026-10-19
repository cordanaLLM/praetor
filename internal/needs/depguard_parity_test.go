// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

type parityCase struct {
	name       string
	config     string
	enableOnly bool
}

var parityCases = []parityCase{
	{
		name: "original_deny",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: original
          deny:
            - pkg: "example.com/fix/a"
`,
		enableOnly: true,
	},
	{
		name: "strict_mode",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: strict
          allow:
            - "$gostd"
            - "example.com/fix/a"
`,
		enableOnly: true,
	},
	{
		name: "lax_mode",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: lax
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: true,
	},
	{
		name: "overlap_allow_longer",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: strict
          allow:
            - "example.com/fix/a/b"
          deny:
            - pkg: "example.com/fix/a"
`,
		enableOnly: true,
	},
	{
		name: "overlap_deny_longer",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: strict
          allow:
            - "example.com/fix/a"
          deny:
            - pkg: "example.com/fix/a/b"
`,
		enableOnly: true,
	},
	{
		name: "exact_dollar_allow",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: strict
          allow:
            - "example.com/fix/a$"
`,
		enableOnly: true,
	},
	{
		name: "exact_dollar_deny",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: lax
          deny:
            - pkg: "example.com/fix/a$"
`,
		enableOnly: true,
	},
	{
		name: "gostd_allow",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: strict
          allow:
            - "$gostd"
`,
		enableOnly: true,
	},
	{
		name: "gostd_deny",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: lax
          deny:
            - pkg: "$gostd"
`,
		enableOnly: true,
	},
	{
		name: "files_not_test",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          files:
            - "$all"
            - "!$test"
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: true,
	},
	{
		name: "files_test_only",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          files:
            - "$test"
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: true,
	},
	{
		name: "files_only_neg",
		config: `version: "2"
linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          files:
            - "!$test"
          deny:
            - pkg: "example.com/fix/a"
`,
		enableOnly: true,
	},
	{
		name: "default_all_enabled",
		config: `version: "2"
linters:
  default: all
  disable:
    - errcheck
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: true,
	},
	{
		name: "default_all_disabled",
		config: `version: "2"
linters:
  default: all
  disable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: false,
	},
	{
		name: "not_enabled",
		config: `version: "2"
linters:
  enable:
    - errcheck
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: "example.com/fix/c"
`,
		enableOnly: false,
	},
	{
		name: "no_rules",
		config: `version: "2"
linters:
  enable:
    - depguard
`,
		enableOnly: true,
	},
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if _, err := os.Stat(filepath.Join(dir, "tools", "lint", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repository root containing tools/lint/go.mod")
	return ""
}

func setupParityFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	writeFixture(t, tmp, "go.mod", "module example.com/fix\n\ngo 1.24\n")

	pkgs := []string{"a", "a/b", "c"}
	for _, p := range pkgs {
		name := filepath.Base(p)
		writeFixture(t, tmp, filepath.Join(p, name+".go"), "package "+name+"\nfunc F() {}\n")
	}

	mainContent := `package main

import (
	_ "example.com/fix/a"
	_ "example.com/fix/a/b"
	_ "example.com/fix/c"
	_ "os/exec"
	_ "reflect"
)

func main() {}
`
	writeFixture(t, tmp, "main.go", mainContent)

	testContent := `package main

import (
	_ "example.com/fix/a"
	_ "example.com/fix/c"
	_ "reflect"
)
`
	writeFixture(t, tmp, "main_test.go", testContent)
	return tmp
}

func runOracleCommand(ctx context.Context, repoRoot, dir string, enableOnly bool) ([]byte, error) {
	toolsMod := filepath.Join(repoRoot, "tools", "lint", "go.mod")
	// golangci-lint refuses to start while another instance on the host holds its lock ("parallel golangci-lint is
	// running"); a parity case must not fail because some other build lints at the same time.
	args := []string{"tool", "-modfile=" + toolsMod, "golangci-lint", "run", "--allow-parallel-runners"}
	if enableOnly {
		args = append(args, "--enable-only", "depguard")
	}
	args = append(args, "./...")
	res, err := util.RunCommandBytes(ctx, dir, "go", util.MaxCommandOutputBytes, args...)
	combined := append(res.Stdout, res.Stderr...)
	return combined, err
}

func parseOracleOutput(output []byte) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	re := regexp.MustCompile(`([^:\s]+\.go):\d+:\d+: import '([^']+)' is not allowed`)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		m := re.FindStringSubmatch(line)
		if len(m) == 3 {
			file := filepath.Base(m[1])
			imp := m[2]
			if result[file] == nil {
				result[file] = make(map[string]bool)
			}
			result[file][imp] = true
		}
	}
	return result
}

func fileImports() map[string][]string {
	return map[string][]string{
		"main.go": {
			"example.com/fix/a",
			"example.com/fix/a/b",
			"example.com/fix/c",
			"os/exec",
			"reflect",
		},
		"main_test.go": {
			"example.com/fix/a",
			"example.com/fix/c",
			"reflect",
		},
	}
}

// oracleBuildBound covers the first `go tool` run, which builds the pinned linter from the
// module cache; a probe bound of seconds skipped the parity test on every cold runner.
const oracleBuildBound = 5 * time.Minute

func verifyOracleRunnable(t *testing.T, repoRoot string) {
	checkCtx, checkCancel := context.WithTimeout(t.Context(), oracleBuildBound)
	defer checkCancel()
	toolsMod := filepath.Join(repoRoot, "tools", "lint", "go.mod")
	out, err := util.RunCommand(checkCtx, repoRoot, "go", "tool", "-modfile="+toolsMod, "golangci-lint", "version")
	if err != nil {
		t.Skipf("cannot run pinned golangci-lint: %v\noutput: %s", err, out)
	}
}

func TestDepguardParityWithOracle(t *testing.T) {
	repoRoot := findRepoRoot(t)
	verifyOracleRunnable(t, repoRoot)
	fixtureDir := setupParityFixture(t)
	filesMap := fileImports()

	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			runSingleParityCase(t, tc, repoRoot, fixtureDir, filesMap)
		})
	}
}

func runSingleParityCase(t *testing.T, tc parityCase, repoRoot, fixtureDir string, filesMap map[string][]string) {
	cfgPath := filepath.Join(fixtureDir, ".golangci.yml")
	if err := os.WriteFile(cfgPath, []byte(tc.config), 0o600); err != nil {
		t.Fatal(err)
	}

	runCtx, runCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer runCancel()

	out, err := runOracleCommand(runCtx, repoRoot, fixtureDir, tc.enableOnly)
	if runCtx.Err() != nil {
		t.Fatalf("oracle execution timed out: %v", runCtx.Err())
	}
	if err != nil && !strings.Contains(string(out), "issues") && !strings.Contains(string(out), "depguard") {
		// The probe proved the linter runs, so an error here is a broken fixture or configuration,
		// never a reason to skip.
		t.Fatalf("pinned linter failed on case %s: %v\noutput: %s", tc.name, err, string(out))
	}

	oracleReported := parseOracleOutput(out)

	loadCtx, loadCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer loadCancel()
	settings, err := config.LoadDepguardSettings(loadCtx, cfgPath)
	if err != nil {
		t.Fatalf("LoadDepguardSettings failed: %v", err)
	}
	if settings.Enabled && len(settings.Rules) == 0 {
		settings.Rules = map[string]config.DepguardRuleConfig{
			"main": {
				ListMode: "original",
				Allow:    []string{"$gostd"},
			},
		}
	}

	assertParityDecisions(t, settings, filesMap, oracleReported, string(out))
}

func assertParityDecisions(t *testing.T, settings *config.DepguardSettings, filesMap map[string][]string, oracleReported map[string]map[string]bool, out string) {
	for file, imports := range filesMap {
		for _, imp := range imports {
			oracleDeclined := oracleReported[file] != nil && oracleReported[file][imp]
			var ourDeclined bool
			if settings.Enabled {
				declined, _, _ := checkDepguard(settings, []string{file}, imp)
				ourDeclined = declined
			}
			if ourDeclined != oracleDeclined {
				t.Errorf("%s in %s: our decision = %v, oracle decision = %v (oracle out: %s)",
					imp, file, ourDeclined, oracleDeclined, out)
			}
		}
	}
}
