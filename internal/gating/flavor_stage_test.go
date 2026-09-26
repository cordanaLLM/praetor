package gating

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goLibraryRepo builds a repository the flavor catalog detects as go-library: a go.mod and
// an internal/ directory, all seven required templates, and both required settings. A case
// overwrites one file and reads the effect off the stage's verdict.
func goLibraryRepo(t *testing.T, settings map[string]string) string {
	t.Helper()
	files := map[string]string{
		"go.mod":                     "module example.com/fixture\n\ngo 1.25\n",
		"internal/keep.go":           "package internal\n",
		".standards.yaml":            "version: 1\n",
		".standards.lock":            "version: 1\n",
		".golangci.yml":              "version: \"2\"\n",
		".github/workflows/ci.yml":   "name: ci\non: push\njobs:\n  test:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make verify-all\n",
		".workingdir/STATE.md":       "# state\n",
		".workingdir/BUGS.md":        "# bugs\n",
		".workingdir/QUESTIONS.md":   "# questions\n",
		"lefthook.yml":               "pre-commit:\n  commands:\n    gofmt:\n      run: gofmt -l .\n",
		".github/rulesets/main.json": "{\"name\": \"main\", \"enforcement\": \"active\"}\n",
	}
	for name, body := range settings {
		files[name] = body
	}
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestRunFlavorStage_Positive_ConformingRepositoryPasses(t *testing.T) {
	msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: goLibraryRepo(t, nil)})
	if err != nil {
		t.Fatalf("a conforming repository must pass the stage: %v", err)
	}
	if msg != "" {
		t.Errorf("a passing stage carries no message, got %q", msg)
	}
}

// TestRunFlavorStage_Negative_FailureNamesTheInvalidSettings covers the report an operator
// gets when the push is already blocked. Settings are validated rather than counted, so a
// repository can fail this stage with every template present: two unparsable settings put
// this fixture at 7/9 = 77.8%. "0 missing templates" alone names no file to fix.
func TestRunFlavorStage_Negative_FailureNamesTheInvalidSettings(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{
		"lefthook.yml":               "pre-commit: [unterminated\n",
		".github/rulesets/main.json": "not json at all",
	})

	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err == nil {
		t.Fatalf("two unparsable settings must fail the stage")
	}
	for _, want := range []string{"0 missing templates", "lefthook.yml", ".github/rulesets/main.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure must name %q, got %q", want, err)
		}
	}
}

// TestRunFlavorStage_Boundary_OneInvalidSettingStillClearsTheBar pins the other side: a
// single unparsable setting costs 1/9 and leaves the repository at 88.9%, above the bar, so
// the stage passes and reports nothing.
func TestRunFlavorStage_Boundary_OneInvalidSettingStillClearsTheBar(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{"lefthook.yml": "pre-commit: [unterminated\n"})

	msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err != nil {
		t.Fatalf("88.9%% clears the 80%% bar: %v", err)
	}
	if msg != "" {
		t.Errorf("a passing stage carries no message, got %q", msg)
	}
}

// TestRunFlavorStage_Boundary_CancelledContextIsNotAVerdict keeps the stage's own I/O
// contract asserted: a cancelled context fails the stage rather than auditing anyway.
func TestRunFlavorStage_Boundary_CancelledContextIsNotAVerdict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runFlavorStage(ctx, &stageConfig{repoDir: goLibraryRepo(t, nil)}); err == nil {
		t.Fatalf("a cancelled context must not produce a pass")
	}
}
