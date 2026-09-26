package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathDefaultSemantics(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{"", "AGENTS.md", filepath.Join(root, "AGENTS.md"), root} {
		for _, args := range []map[string]any{nil, {"source": ""}, {"source": nil}, {"source": " \t "}} {
			got, err := srv.resolvePath(args, "source", def)
			want := def
			if !filepath.IsAbs(want) {
				want = filepath.Join(root, want)
			}
			if err != nil || got != filepath.Clean(want) {
				t.Fatalf("default %q: got %q, %v; want %q", def, got, err, want)
			}
		}
	}
}

func TestResolvePathRejectsEscapingDefaults(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	srv, err := NewServer(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", ".standards.yaml"} {
		target := filepath.Join(outside, name)
		if err := os.WriteFile(target, []byte("protected\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		for _, def := range []string{name, filepath.Join(root, name)} {
			for _, args := range []map[string]any{nil, {"path": ""}, {"path": nil}, {"path": " \t "}} {
				if _, err := srv.resolvePath(args, "path", def); !errors.Is(err, ErrOutsideRoot) {
					t.Errorf("default %q with %v: want ErrOutsideRoot, got %v", def, args, err)
				}
			}
		}
	}
	if _, err := srv.resolvePath(nil, "path", outside); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("absolute outside default: want ErrOutsideRoot, got %v", err)
	}
	srv.opts.AllowOutsideRoot = true
	if got, err := srv.resolvePath(nil, "path", filepath.Join(root, "AGENTS.md")); err != nil || got == "" {
		t.Fatalf("explicit outside-root opt-in must permit the default: %q, %v", got, err)
	}
}

// symlinkCase names one symlinked compile-context output: where the link points, whether the
// link is the output file itself or its .codex directory, and the refusal the call must return.
type symlinkCase struct {
	target      string // "outside", "outside-allowed" (AllowOutsideRoot) or "in-root"
	directory   bool
	verify      bool
	wantRefusal string
}

func symlinkCases() []symlinkCase {
	var cases []symlinkCase
	for _, target := range []string{"outside", "outside-allowed", "in-root"} {
		for _, directory := range []bool{false, true} {
			for _, verify := range []bool{false, true} {
				prefix, refusal := "target .codex/rules.md: ", "compiled output must be a regular file, never a symlink or directory"
				if verify {
					prefix, refusal = "target .codex/rules.md missing or unreadable: ", "source must be regular"
				}
				if directory {
					refusal = "path component must be a directory, never a symlink"
				}
				cases = append(cases, symlinkCase{target: target, directory: directory, verify: verify, wantRefusal: prefix + refusal})
			}
		}
	}
	return cases
}

// A symlinked output, or a symlinked directory above one, is refused on write and on verify,
// wherever it points and with or without --allow-outside-root, and a refused write leaves the
// outputs before it unchanged. The CLI writer never followed one; the MCP tool now shares it.
// For verify the link's target holds exactly the compiled content, so following it would
// pass: the refusal is the only way the call can fail.
func TestCompileContextRejectsSymlinkedOutputDescendants(t *testing.T) {
	for _, c := range symlinkCases() {
		t.Run(fmt.Sprintf("%s/directory=%t/verify=%t", c.target, c.directory, c.verify), func(t *testing.T) {
			srv, root := newFixtureServer(t)
			srv.opts.AllowOutsideRoot = c.target == "outside-allowed"
			out := filepath.Join(root, "out")
			expectText(t, "seed", callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out"}), "[COMPILED] .codex/rules.md")
			compiled := readPathFixture(t, filepath.Join(out, ".codex", "rules.md"))
			if err := os.RemoveAll(filepath.Join(out, ".codex")); err != nil {
				t.Fatal(err)
			}
			firstOutput := filepath.Join(out, "CLAUDE.md")
			if !c.verify {
				writePathFixture(t, firstOutput, "first output unchanged\n")
			}
			firstBefore := readPathFixture(t, firstOutput)
			redirected := filepath.Join(root, "redirected")
			if c.target != "in-root" {
				redirected = t.TempDir()
			}
			markerBefore := "protected\n"
			if c.verify {
				markerBefore = compiled
			}
			marker := filepath.Join(redirected, "rules.md")
			writePathFixture(t, marker, markerBefore)
			link, target := filepath.Join(out, ".codex", "rules.md"), marker
			if c.directory {
				link, target = filepath.Join(out, ".codex"), redirected
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out", "verify_only": c.verify})
			expectError(t, "symlinked output", res, c.wantRefusal)
			assertPathFixture(t, marker, markerBefore)
			assertPathFixture(t, firstOutput, firstBefore)
		})
	}
}

// Replaces the symlink half of the deleted TestCompileContextPermitsAllowedOutputDescendants
// with what stays permitted: real output directories below target_dir, and a target_dir outside
// the server root once --allow-outside-root opts in, are written over and then verify in sync.
func TestCompileContextWritesRealOutputDescendants(t *testing.T) {
	for _, allowOutside := range []bool{false, true} {
		t.Run(fmt.Sprintf("allowOutside=%t", allowOutside), func(t *testing.T) {
			srv, root := newFixtureServer(t)
			srv.opts.AllowOutsideRoot = allowOutside
			targetArg, out := "out", filepath.Join(root, "out")
			if allowOutside {
				out = t.TempDir()
				targetArg = out
			}
			stale := filepath.Join(out, ".codex", "rules.md")
			writePathFixture(t, stale, "replace me\n")
			written := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": targetArg})
			expectText(t, "real output directory", written, "[COMPILED] .codex/rules.md")
			if got := readPathFixture(t, stale); got == "replace me\n" {
				t.Errorf("%s was not rewritten", stale)
			}
			verified := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": targetArg, "verify_only": true})
			expectText(t, "real output verification", verified, "100% in sync")
		})
	}
}

func readPathFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writePathFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertPathFixture(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Errorf("%s: got %q, %v; want %q", path, data, err, want)
	}
}
