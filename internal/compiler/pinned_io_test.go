package compiler

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestCompilerPinnedSourceAndOutputs(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeCanonicalFixture(t, outside, "# Outside source\n")
	source := filepath.Join(root, "AGENTS.md")
	if err := os.Symlink(outside, source); err != nil {
		t.Fatal(err)
	}
	tr := NewTranspiler()
	if _, err := tr.CompileContext(t.Context(), source); err == nil {
		t.Fatal("followed canonical source symlink")
	}
	result, err := tr.CompileContent("# Policy\n")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "CLAUDE.md")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputsContext(t.Context(), result, root); err == nil {
		t.Fatal("followed output symlink")
	}
	actual, err := os.ReadFile(outside)
	if err != nil || string(actual) != "# Outside source\n" {
		t.Fatalf("outside source changed: %q %v", actual, err)
	}
}

func TestCompilerRejectsInvalidOutputsBeforeWrites(t *testing.T) {
	root := t.TempDir()
	tr := NewTranspiler()
	for _, path := range []string{"../outside.md", "/absolute.md", "nested/../outside.md", "."} {
		result := &CompileResult{Files: []TargetFile{{RelativePath: "first.md", Content: "first"}, {RelativePath: path, Content: "bad"}}}
		if err := tr.WriteOutputsContext(t.Context(), result, root); err == nil {
			t.Fatalf("accepted path %q", path)
		}
		if _, err := os.Stat(filepath.Join(root, "first.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wrote before validating all destinations: %v", err)
		}
	}
	var absent context.Context
	if err := tr.WriteOutputsContext(absent, &CompileResult{}, root); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := tr.WriteOutputsContext(ctx, &CompileResult{}, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestCompilerBoundedSourceAndPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, strings.Repeat("x", contextopt.MaxSourceBytes+1))
	tr := NewTranspiler()
	if _, err := tr.CompileContext(t.Context(), source); err == nil {
		t.Fatal("oversized source accepted")
	}
	writeCanonicalFixture(t, source, "# Policy\n")
	result, err := tr.CompileContext(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "CLAUDE.md")
	writeCanonicalFixture(t, target, "old")
	if err := tr.WriteOutputsContext(t.Context(), result, root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private permissions widened: %o", info.Mode().Perm())
	}
	for _, file := range result.Files {
		actual, err := os.ReadFile(filepath.Join(root, file.RelativePath))
		if err != nil || !bytes.Equal(actual, []byte(file.Content)) {
			t.Fatalf("projection mismatch %s: %v", file.RelativePath, err)
		}
	}
	if err := tr.VerifyContext(t.Context(), source, root); err != nil {
		t.Fatal(err)
	}
}
