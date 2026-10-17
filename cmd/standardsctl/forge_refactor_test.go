package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type refactorFixture struct {
	dir  string
	env  []string
	base string
}

func newRefactorFixture(t *testing.T) refactorFixture {
	t.Helper()
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "go.mod", "module example.test/refactor\n\ngo 1.24\n")
	writeFixtureFile(t, dir, "value.go", "package sample\n\nfunc Value() int { return 1 }\n")
	writeFixtureFile(t, dir, "value_test.go", "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n got := Value()\n if got != 1 { t.Fatalf(\"got %d\", got) }\n}\n")
	gitCommitAll(t, dir, env, "chore: add Go fixture")
	base := strings.TrimSpace(string(runFixtureGitOutput(t, dir, env, "rev-parse", "HEAD")))
	return refactorFixture{dir: dir, env: env, base: base}
}

func runFixtureGitOutput(t *testing.T, dir string, env []string, args ...string) []byte {
	t.Helper()
	out, err := runFixtureGit(t, dir, env, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return []byte(out)
}

func commitRefactorFixture(t *testing.T, f refactorFixture, message string) string {
	t.Helper()
	gitCommitAll(t, f.dir, f.env, message)
	return strings.TrimSpace(string(runFixtureGitOutput(t, f.dir, f.env, "rev-parse", "HEAD")))
}

func runRefactorFixture(t *testing.T, f refactorFixture, tests bool) (string, error) {
	t.Helper()
	args := []string{"refactor-check", "--repo=" + f.dir, "--base=" + f.base, "--head=HEAD"}
	if tests {
		args = append(args, "--run-tests")
	}
	return captureStdout(t, func() error { return dispatchCommand("forge", args) })
}

func TestForgeRefactorCheck_Positive_FunctionRenameAndGreenSuite(t *testing.T) {
	f := newRefactorFixture(t)
	writeFixtureFile(t, f.dir, "value.go", "package sample\n\nfunc Result() int { return 1 }\n")
	writeFixtureFile(t, f.dir, "value_test.go", "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n got := Result()\n if got != 1 { t.Fatalf(\"got %d\", got) }\n}\n")
	commitRefactorFixture(t, f, "refactor: rename value function")
	out, err := runRefactorFixture(t, f, true)
	if err != nil || !strings.Contains(out, "PASS ") || !strings.Contains(out, "touched package tests pass") {
		t.Fatalf("function rename with green suite: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Negative_AssertionEdited(t *testing.T) {
	f := newRefactorFixture(t)
	testSource := strings.Replace(readFixtureFile(t, f.dir, "value_test.go"),
		`if got != 1 { t.Fatalf("got %d", got) }`,
		"if got != 1 {\n  t.Fatalf(\n   \"got %d\",\n   got,\n  )\n }", 1)
	writeFixtureFile(t, f.dir, "value_test.go", strings.Replace(testSource, "\"got %d\"", "\"want %d\"", 1))
	commitRefactorFixture(t, f, "refactor: edit assertion")
	out, err := runRefactorFixture(t, f, false)
	if err == nil || !strings.Contains(out, "FAIL ") || !strings.Contains(out, "value_test.go:") || !strings.Contains(out, "assertion line") {
		t.Fatalf("edited assertion must fail with a location: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Negative_TestFunctionRemoved(t *testing.T) {
	f := newRefactorFixture(t)
	writeFixtureFile(t, f.dir, "value_test.go", "package sample\n\nimport \"testing\"\n")
	commitRefactorFixture(t, f, "refactor: remove test")
	out, err := runRefactorFixture(t, f, false)
	if err == nil || !strings.Contains(out, "removed test function TestValue") {
		t.Fatalf("removed test function must fail: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Negative_FailingSuiteAtCommit(t *testing.T) {
	f := newRefactorFixture(t)
	writeFixtureFile(t, f.dir, "value.go", "package sample\n\nfunc Value() int { return 2 }\n")
	commitRefactorFixture(t, f, "refactor: change value result")
	out, err := runRefactorFixture(t, f, true)
	if err == nil || !strings.Contains(out, "touched package tests failed or could not run") {
		t.Fatalf("failing suite must fail the gate: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Positive_NonRefactorAssertionChangeSkipped(t *testing.T) {
	f := newRefactorFixture(t)
	writeFixtureFile(t, f.dir, "value_test.go", strings.Replace(readFixtureFile(t, f.dir, "value_test.go"), "got %d", "value %d", 1))
	commitRefactorFixture(t, f, "feat: update test message")
	out, err := runRefactorFixture(t, f, false)
	if err != nil || !strings.Contains(out, "SKIPPED 1 non-refactor commit(s)") || strings.Contains(out, "FAIL ") {
		t.Fatalf("feat commit must be skipped: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Positive_PureTestFileRename(t *testing.T) {
	f := newRefactorFixture(t)
	if err := os.Rename(filepath.Join(f.dir, "value_test.go"), filepath.Join(f.dir, "renamed_test.go")); err != nil {
		t.Fatal(err)
	}
	commitRefactorFixture(t, f, "refactor: rename test file")
	out, err := runRefactorFixture(t, f, false)
	if err != nil || !strings.Contains(out, "PASS ") {
		t.Fatalf("pure test-file rename must pass: %v\n%s", err, out)
	}
}

func TestForgeRefactorCheck_Boundary_EmptyAndInvalidRange(t *testing.T) {
	f := newRefactorFixture(t)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("forge", []string{"refactor-check", "--repo=" + f.dir, "--base=HEAD", "--head=HEAD"})
	})
	if err != nil || !strings.Contains(out, "SKIPPED 0 non-refactor commit(s)") {
		t.Fatalf("empty range: %v\n%s", err, out)
	}
	if err := dispatchCommand("forge", []string{"refactor-check", "--repo=" + f.dir}); err == nil {
		t.Fatal("missing base must fail")
	}
}

func TestForgeRefactorCheck_Boundary_RootCommit(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	if _, err := runFixtureGit(t, dir, env, "commit", "--amend", "-q", "-m", "refactor: root fixture"); err != nil {
		t.Fatalf("amend root fixture: %v", err)
	}
	root := strings.TrimSpace(string(runFixtureGitOutput(t, dir, env, "rev-list", "--max-parents=0", "HEAD")))
	out, err := captureStdout(t, func() error {
		return dispatchCommand("forge", []string{
			"refactor-check", "--repo=" + dir, "--base=" + root, "--head=HEAD", "--include-base",
		})
	})
	if err != nil || !strings.Contains(out, "PASS ") {
		t.Fatalf("root refactor commit: %v\n%s", err, out)
	}
}
