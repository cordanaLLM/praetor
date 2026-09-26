package flavor_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// BUG-581 is "docker build . fails", so the builder stage's RUN is executed here rather than
// only read. It compiled ./cmd/... into one file, which fails on a module with two commands
// ("cannot write multiple packages to non-directory") and on a service with its main package
// anywhere but cmd/ ("lstat ./cmd/: no such file or directory"). The case table is those
// layouts; the instruction under test is the scaffolded one, with only its output path moved
// out of /bin.

// maxBuilderLines bounds the scan for the builder's RUN instruction (HISS-02).
const maxBuilderLines = 256

// builderRunScript returns the scaffolded Dockerfile's `RUN set -eu;` instruction as the one
// line /bin/sh -c receives: continuation lines joined the way the Dockerfile parser joins them.
func builderRunScript(t *testing.T, dockerfile string) string {
	t.Helper()
	lines := strings.Split(dockerfile, "\n")
	var script strings.Builder
	collecting := false
	for i := 0; i < len(lines) && i < maxBuilderLines; i++ {
		line := lines[i]
		if !collecting && !strings.HasPrefix(line, "RUN set -eu;") {
			continue
		}
		collecting = true
		body, continued := strings.CutSuffix(line, "\\")
		script.WriteString(body)
		if !continued {
			return strings.TrimPrefix(script.String(), "RUN ")
		}
	}
	t.Fatalf("the scaffolded Dockerfile has no complete `RUN set -eu;` builder instruction:\n%s", dockerfile)
	return ""
}

// posixShell returns sh, or skips where the builder's shell cannot be reproduced.
func posixShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the builder stage runs /bin/sh inside a Linux image; a Windows host has no POSIX sh to run it with")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh is not available to execute the builder instruction: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not available to execute the builder instruction: %v", err)
	}
	return shell
}

// goModule writes a module with one package per entry of mains (a main package) and libs
// (a library package), each at the slash path it names ("." for the module root).
func goModule(t *testing.T, mains, libs []string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/widget\n\ngo 1.27\n")
	for _, dir := range mains {
		write(dir+"/main.go", "package main\n\nfunc main() {}\n")
	}
	for _, dir := range libs {
		write(dir+"/lib.go", "package "+filepath.Base(dir)+"\n\nconst X = 1\n")
	}
	return root
}

// runBuilder executes the builder instruction in module with MAIN_PACKAGE set as a build
// argument would set it, and reports whether the binary landed at the output path.
func runBuilder(t *testing.T, shell, script, module, mainPackage string) (built bool, output string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "app")
	script = strings.ReplaceAll(script, "-o /bin/app", "-o '"+binary+"'")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", script)
	cmd.Dir = module
	cmd.Env = append(os.Environ(), "MAIN_PACKAGE="+mainPackage, "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if _, statErr := os.Stat(binary); err == nil && statErr == nil {
		return true, string(out)
	}
	return false, string(out)
}

func TestScaffoldedDockerfileBuilderCompilesTheModulesMainPackage(t *testing.T) {
	shell := posixShell(t)
	script := builderRunScript(t, scaffoldInto(t, "go-service", "Dockerfile")["Dockerfile"])
	if !strings.Contains(script, "-o /bin/app ") {
		t.Fatalf("the builder no longer writes /bin/app, which the runtime stage copies: %s", script)
	}
	cases := []struct {
		name        string
		mains, libs []string
		mainPackage string
		wantBuilt   bool
		wantMessage string
	}{
		{name: "positive: one command under cmd/", mains: []string{"cmd/widget"}, libs: []string{"internal/lib"}, wantBuilt: true},
		{name: "positive: main package at the root and no cmd/", mains: []string{"."}, wantBuilt: true},
		{name: "positive: two commands, one chosen", mains: []string{"cmd/a", "cmd/b"}, mainPackage: "./cmd/b", wantBuilt: true},
		{name: "negative: two commands, none chosen", mains: []string{"cmd/a", "cmd/b"},
			wantMessage: "has 2 main packages: example.com/widget/cmd/a example.com/widget/cmd/b"},
		{name: "boundary: no main package", libs: []string{"lib"}, wantMessage: "has 0 main packages: none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built, output := runBuilder(t, shell, script, goModule(t, tc.mains, tc.libs), tc.mainPackage)
			if built != tc.wantBuilt {
				t.Fatalf("built %v, want %v; output:\n%s", built, tc.wantBuilt, output)
			}
			if tc.wantMessage != "" && !strings.Contains(output, tc.wantMessage) {
				t.Errorf("the failure does not name what it found (want %q):\n%s", tc.wantMessage, output)
			}
		})
	}
}
