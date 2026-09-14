package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestCompileContextRejectsEscapingOutputDescendants(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, verify := range []bool{false, true} {
			t.Run(fmt.Sprintf("directory=%t/verify=%t", directory, verify), func(t *testing.T) {
				srv, root := newFixtureServer(t)
				out, outside := filepath.Join(root, "out"), t.TempDir()
				marker := filepath.Join(outside, "rules.md")
				writePathFixture(t, marker, "protected\n")
				firstOutput := filepath.Join(out, "CLAUDE.md")
				writePathFixture(t, firstOutput, "first output unchanged\n")
				link, target := filepath.Join(out, ".codex", "rules.md"), marker
				if directory {
					link, target = filepath.Join(out, ".codex"), outside
				}
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				res := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out", "verify_only": verify})
				if !res.IsError || !strings.Contains(res.Content[0].Text, "outside the server root") {
					t.Errorf("escaping output must fail confinement, got %+v", res)
				}
				assertPathFixture(t, marker, "protected\n")
				assertPathFixture(t, firstOutput, "first output unchanged\n")
			})
		}
	}
}

func TestCompileContextPermitsAllowedOutputDescendants(t *testing.T) {
	for _, allowOutside := range []bool{false, true} {
		for _, directory := range []bool{false, true} {
			t.Run(fmt.Sprintf("allowOutside=%t/directory=%t", allowOutside, directory), func(t *testing.T) {
				srv, root := newFixtureServer(t)
				srv.opts.AllowOutsideRoot = allowOutside
				out, redirected := filepath.Join(root, "out"), filepath.Join(root, "redirected")
				if allowOutside {
					redirected = t.TempDir()
				}
				marker := filepath.Join(redirected, "rules.md")
				writePathFixture(t, marker, "replace me\n")
				link, target := filepath.Join(out, ".codex", "rules.md"), marker
				if directory {
					link, target = filepath.Join(out, ".codex"), redirected
				}
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				written := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out"})
				expectText(t, "allowed output symlink", written, "[COMPILED] .codex/rules.md")
				verified := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out", "verify_only": true})
				expectText(t, "allowed output verification", verified, "100% in sync")
			})
		}
	}
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
