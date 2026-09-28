package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func confinedKit() *FrameworkKitConfig {
	return &FrameworkKitConfig{KitName: "confined", Language: "go", Version: "1.0.0", Rules: []string{"stay inside"}}
}

// Positive: every generated file lands below the output directory.
func TestCompileFrameworkAssetsConfined_Positive(t *testing.T) {
	out := t.TempDir()
	res, err := CompileFrameworkAssets(context.Background(), confinedKit(), out)
	if err != nil {
		t.Fatalf("CompileFrameworkAssets: %v", err)
	}
	for _, path := range []string{res.LLMsTxtPath, res.LLMsFullTxtPath, res.AgentRulePath, res.Templates["README.md"]} {
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s = %v, %v; want a regular file", path, info, err)
		}
	}
}

// Negative: an existing .agents directory linked outside the output directory is refused
// before the agent rule is written through it (BUG-826).
func TestCompileFrameworkAssetsConfined_Negative_EscapingAgentsLink(t *testing.T) {
	out := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(out, ".agents")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CompileFrameworkAssets(context.Background(), confinedKit(), out); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("escaping .agents = %v, want ErrPathEscapesRoot", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory = %v, %v; want nothing written through the link", entries, err)
	}
}

// Boundary: a templates directory that is a relative link staying inside the output
// directory is followed; the confinement edge is the output directory itself.
func TestCompileFrameworkAssetsConfined_Boundary_InRootLinkedTemplates(t *testing.T) {
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "kit", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("kit", "templates"), filepath.Join(out, "templates")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CompileFrameworkAssets(context.Background(), confinedKit(), out); err != nil {
		t.Fatalf("in-root linked templates = %v, want the write to follow it", err)
	}
	if _, err := os.Stat(filepath.Join(out, "kit", "templates", "README.md")); err != nil {
		t.Fatalf("template not written through the in-root link: %v", err)
	}
}
