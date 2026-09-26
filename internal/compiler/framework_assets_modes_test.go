package compiler

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestCompileFrameworkAssets_Positive_TrackedModes pins BUG-839: framework assets are generated
// files a repository commits, so every output directory and file carries the tracked-file modes
// rather than the owner-only ones the private working directory uses.
func TestCompileFrameworkAssets_Positive_TrackedModes(t *testing.T) {
	kit := &FrameworkKitConfig{
		KitName:     "modes",
		Language:    "go",
		Version:     "1.0.0",
		Description: "mode fixture",
		Rules:       []string{"Keep generated files world-readable"},
	}
	outDir := filepath.Join(t.TempDir(), "modes-out")
	res, err := CompileFrameworkAssets(t.Context(), kit, outDir)
	if err != nil {
		t.Fatalf("CompileFrameworkAssets: %v", err)
	}
	testsupport.RequireCreatedMode(t, outDir, 0o755)
	for _, path := range []string{res.LLMsTxtPath, res.LLMsFullTxtPath, res.AgentRulePath} {
		testsupport.RequireCreatedMode(t, path, 0o644)
		testsupport.RequireCreatedMode(t, filepath.Dir(path), 0o755)
	}
	if len(res.Templates) == 0 {
		t.Fatal("no starter templates were generated")
	}
	for _, path := range res.Templates {
		testsupport.RequireCreatedMode(t, path, 0o644)
	}
}
