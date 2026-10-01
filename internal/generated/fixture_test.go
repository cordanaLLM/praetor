// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// fixtureManifest declares two artefacts rendered from src/*.txt: a whole file and a block of a
// hand-edited guide. No built-in artefact applies to the fixture.
const fixtureManifest = `version: 1
generated:
  artefacts:
    - name: "table"
      paths:
        - "out/table.txt"
      command: ["render", "table"]
      sources:
        - "src/*.txt"
    - name: "guide block"
      paths:
        - "docs/guide.md"
      block:
        start: "<!-- gen:start -->"
        end: "<!-- gen:end -->"
      command: ["render", "block"]
      sources:
        - "src/*.txt"
`

// guideText is the hand-edited guide around a generated block holding body.
func guideText(intro, body string) string {
	return "# Guide\n\n" + intro + "\n\n<!-- gen:start -->\n" + body + "\n<!-- gen:end -->\n\nOutro.\n"
}

// fixture is a repository whose main branch holds fresh artefacts.
type fixture struct {
	dir  string
	env  []string
	main string
}

// newFixture commits the manifest, one source and its fresh renderings on main.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	f := &fixture{dir: t.TempDir(), env: testsupport.HermeticGitEnv(t)}
	f.write(t, ".standards.yaml", fixtureManifest)
	f.write(t, ".gitignore", "/.standards/worktrees/\n")
	f.write(t, "src/a.txt", "alpha\n")
	f.write(t, "out/table.txt", "ALPHA\n")
	f.write(t, "docs/guide.md", guideText("Intro.", "ALPHA"))
	f.git(t, "init", "-q", "-b", "main")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "base")
	f.main = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
	return f
}

// write writes one fixture file.
func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// read reads one fixture file.
func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// git runs one fixture git command under the hermetic environment.
func (f *fixture) git(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = f.dir, f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// branch starts a branch from main, applies files (a nil value removes the file) and commits.
func (f *fixture) branch(t *testing.T, name string, files map[string]*string) {
	t.Helper()
	f.git(t, "checkout", "-q", "-B", name, f.main)
	f.commit(t, name, files)
}

// commit applies files on the current branch and commits them.
func (f *fixture) commit(t *testing.T, message string, files map[string]*string) {
	t.Helper()
	for rel, content := range files {
		if content == nil {
			f.git(t, "rm", "-q", rel)
			continue
		}
		f.write(t, rel, *content)
	}
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", message)
}

// text is a pointer to s, for fixture file maps.
func text(s string) *string { return &s }

// fakeRunner renders the fixture's artefacts from the files in dir: "render table" writes the
// upper-cased sources to out/table.txt, "render block" writes them into the guide's block, and
// "render fail" fails. It records every command it ran.
type fakeRunner struct {
	ran []string
}

func (r *fakeRunner) run(ctx context.Context, dir string, argv, env []string) (util.CommandBytes, error) {
	r.ran = append(r.ran, strings.Join(argv, " "))
	if err := ctx.Err(); err != nil {
		return util.CommandBytes{}, err
	}
	rendered, err := renderSources(dir)
	if err != nil {
		return util.CommandBytes{}, err
	}
	switch strings.Join(argv, " ") {
	case "render table":
		return util.CommandBytes{}, os.WriteFile(filepath.Join(dir, "out", "table.txt"), []byte(rendered+"\n"), 0o644)
	case "render block":
		return renderGuideBlock(dir, rendered)
	}
	return util.CommandBytes{Stderr: []byte("generator exploded")}, errors.New("exit status 3")
}

// mixedRunner runs git commands for real and the fixture's render commands through fake.
func mixedRunner(fake *fakeRunner) Runner {
	return func(ctx context.Context, dir string, argv, env []string) (util.CommandBytes, error) {
		if argv[0] == "git" {
			return execCommand(ctx, dir, argv, env)
		}
		return fake.run(ctx, dir, argv, env)
	}
}

// renderSources upper-cases and joins every src/*.txt in dir, in name order.
func renderSources(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "src", "*.txt"))
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.ToUpper(strings.TrimSpace(string(data))))
	}
	return strings.Join(parts, "\n"), nil
}

// renderGuideBlock replaces the guide's block with rendered.
func renderGuideBlock(dir, rendered string) (util.CommandBytes, error) {
	path := filepath.Join(dir, "docs", "guide.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return util.CommandBytes{}, err
	}
	block := &Block{Start: "<!-- gen:start -->", End: "<!-- gen:end -->"}
	replaced, err := replaceBlock(string(data), block.Start+"\n"+rendered+"\n"+block.End, block)
	if err != nil {
		return util.CommandBytes{}, err
	}
	return util.CommandBytes{}, os.WriteFile(path, []byte(replaced), 0o644)
}

// testContext bounds one test's work.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// resultFor returns the named artefact's result.
func resultFor(t *testing.T, report *Report, name string) Result {
	t.Helper()
	for _, result := range report.Artefacts {
		if result.Name == name {
			return result
		}
	}
	t.Fatalf("no result for %q in %+v", name, report.Artefacts)
	return Result{}
}

// mustProblem fails unless one problem contains every fragment.
func mustProblem(t *testing.T, report *Report, fragments ...string) {
	t.Helper()
	for _, problem := range report.Problems {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(problem, fragment)
		}
		if matched {
			return
		}
	}
	t.Fatalf("no problem contains %q: %q", fragments, report.Problems)
}

// noWorktreeLeft fails when a render worktree or its branch survived.
func noWorktreeLeft(t *testing.T, f *fixture) {
	t.Helper()
	if listed := f.git(t, "worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 1 {
		t.Fatalf("a render worktree survived:\n%s", listed)
	}
	if branches := f.git(t, "branch", "--list", "wt/*"); strings.TrimSpace(branches) != "" {
		t.Fatalf("a render branch survived: %s", branches)
	}
}
