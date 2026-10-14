package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

// harnessGoldens returns every adopted harness golden (TestAdoptedHarnessGolden keeps them equal
// to what adoption renders), by file name.
func harnessGoldens(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "harness-*.golden"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("harness goldens: %v (%d found)", err, len(paths))
	}
	goldens := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		goldens[filepath.Base(path)] = strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	return goldens
}

// adopterText returns n lines of repository text as they follow a merged harness.
func adopterText(n int) string {
	return harnessSeparator + "\n" + strings.Repeat("- keep module tests green.\n", n)
}

// #1232: a harness change that grows the engine block past its own allowance fails here, not in
// every adopted repository. Positive: each golden harness fits EngineBlockMaxLines and is
// recognized as engine text. Boundary: the allowance itself bounds the count.
func TestHarnessFitsEngineBlockAllowance(t *testing.T) {
	for name, harness := range harnessGoldens(t) {
		owned := agentcontext.EngineOwnedLines(harness)
		if owned < agentcontext.EngineBlockReserve || owned > agentcontext.EngineBlockMaxLines {
			t.Errorf("%s: engine block %d lines, want %d..%d", name, owned, agentcontext.EngineBlockReserve, agentcontext.EngineBlockMaxLines)
		}
	}
}

// #1232: adopter text sized for the engine block before it grew (213 lines on
// lusoris/sveltesentio, 9 under the budget) still projects under every current harness; text over
// its own allowance still fails, with both parts in the error.
func TestAdopterAtPreviousBlockSizeStillProjects(t *testing.T) {
	for name, harness := range harnessGoldens(t) {
		if err := validateHarnessProjection(harness + adopterText(210)); err != nil {
			t.Errorf("positive %s: unchanged adopter text rejected: %v", name, err)
		}
		err := validateHarnessProjection(harness + adopterText(260))
		if err == nil || !strings.Contains(err.Error(), "engine block") || !strings.Contains(err.Error(), "adopter allowance") {
			t.Errorf("negative %s: adopter text over its allowance: error = %v", name, err)
		}
	}
}
