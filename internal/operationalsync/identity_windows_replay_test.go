package operationalsync

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// maxAdoptFiles bounds the directory walk of internal/adopt (HISS-02).
const maxAdoptFiles = 2000

// maxBuildHeaderLines bounds the file header lines scanned for build constraints (HISS-02).
const maxBuildHeaderLines = 100

// maxTestDeclsPerFile bounds the AST declarations examined in a test file (HISS-02).
const maxTestDeclsPerFile = 5000

// maxTestFiles bounds the files parsed for Test functions (HISS-02).
const maxTestFiles = 100

// adoptWindowsSkipReason is the durable rationale logged when internal/adopt is skipped on Windows (#1381).
const adoptWindowsSkipReason = "identity is read from .standards.yaml, which is platform-neutral; Linux and macOS replay internal/adopt in full; on Windows only its Windows-specific tests replay (#1381)"

// checkBuildConstraintMentionsWindows scans the leading comments of filePath up to package
// declaration for a //go:build directive mentioning windows.
func checkBuildConstraintMentionsWindows(filePath string) (mentions bool, err error) {
	f, openErr := os.Open(filePath)
	if openErr != nil {
		return false, fmt.Errorf("open %s for build constraints: %w", filePath, openErr)
	}
	defer func() {
		err = errors.Join(err, f.Close())
	}()

	scanner := bufio.NewScanner(f)
	for i := 0; i < maxBuildHeaderLines && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			break
		}
		if strings.HasPrefix(line, "//go:build") {
			expr := strings.TrimSpace(strings.TrimPrefix(line, "//go:build"))
			if strings.Contains(expr, "windows") {
				return true, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan %s: %w", filePath, err)
	}
	return false, nil
}

// isWindowsSpecificGoFile checks whether a .go file is Windows-specific by name suffix or //go:build constraint.
func isWindowsSpecificGoFile(filePath, name string) (bool, error) {
	if strings.HasSuffix(name, "_windows.go") || strings.HasSuffix(name, "_windows_test.go") {
		return true, nil
	}
	return checkBuildConstraintMentionsWindows(filePath)
}

func shouldSkipAdoptDir(p, dir, name string) bool {
	return p != dir && (name == "testdata" || strings.HasPrefix(name, "."))
}

// findWindowsSpecificFiles walks dir looking for Windows-specific Go files and returns their
// slash paths with relPrefix prepended, sorted deterministically.
func findWindowsSpecificFiles(dir, relPrefix string) ([]string, error) {
	var matches []string
	walkCount := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walkCount++
		if walkCount > maxAdoptFiles {
			return fmt.Errorf("walking %s exceeded scalar bound of %d entries", dir, maxAdoptFiles)
		}
		if d.IsDir() {
			if shouldSkipAdoptDir(p, dir, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		isWin, err := isWindowsSpecificGoFile(p, d.Name())
		if err != nil {
			return err
		}
		if isWin {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return fmt.Errorf("relative path for %s: %w", p, err)
			}
			slashRel := filepath.ToSlash(rel)
			if relPrefix != "" {
				slashRel = path.Join(relPrefix, slashRel)
			}
			matches = append(matches, slashRel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	return matches, nil
}

// findWindowsAdoptFiles returns the sorted repository-relative slash paths of Windows-specific files under internal/adopt.
func findWindowsAdoptFiles(repoRoot string) ([]string, error) {
	adoptDir := filepath.Join(repoRoot, "internal", "adopt")
	return findWindowsSpecificFiles(adoptDir, "internal/adopt")
}

// checkStaleReplayEntries verifies that every entry in replayList exists on disk and is windows-specific.
func checkStaleReplayEntries(repoRoot string, replayList []string, foundSet map[string]bool) error {
	for i := 0; i < len(replayList) && i < maxTestFiles; i++ {
		e := replayList[i]
		entryPath := filepath.Join(repoRoot, filepath.FromSlash(e))
		info, err := os.Stat(entryPath)
		if os.IsNotExist(err) {
			return fmt.Errorf("replay list entry %s does not exist on disk", e)
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", e, err)
		}
		if info.IsDir() || !foundSet[e] {
			return fmt.Errorf("replay list entry %s is not a windows-specific file", e)
		}
	}
	return nil
}

// verifyWindowsAdoptReplay ensures that the finder result exactly matches replayList, reporting
// missing files or stale entries naming the affected file.
func verifyWindowsAdoptReplay(repoRoot string, replayList []string) error {
	found, err := findWindowsAdoptFiles(repoRoot)
	if err != nil {
		return err
	}
	foundSet := make(map[string]bool, len(found))
	for _, f := range found {
		foundSet[f] = true
	}
	expectedSet := make(map[string]bool, len(replayList))
	for _, e := range replayList {
		expectedSet[e] = true
	}
	for _, f := range found {
		if !expectedSet[f] {
			return fmt.Errorf("windows-specific file %s is missing from replay list", f)
		}
	}
	if err := checkStaleReplayEntries(repoRoot, replayList, foundSet); err != nil {
		return err
	}
	if !slices.Equal(found, replayList) {
		return fmt.Errorf("found files %v do not match replay list %v", found, replayList)
	}
	return nil
}

// parseTestFunctionsInFile parses filePath using go/parser and returns the names of all Test* functions.
func parseTestFunctionsInFile(filePath string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	var tests []string
	for i := 0; i < len(f.Decls) && i < maxTestDeclsPerFile; i++ {
		fn, ok := f.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "Test") {
			tests = append(tests, fn.Name.Name)
		}
	}
	return tests, nil
}

// findTestFunctions extracts all Test functions declared in the listed test files using go/parser.
func findTestFunctions(repoRoot string, relPaths []string) ([]string, error) {
	var allTests []string
	for i := 0; i < len(relPaths) && i < maxTestFiles; i++ {
		rel := relPaths[i]
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		abs := filepath.Join(repoRoot, filepath.FromSlash(rel))
		tests, err := parseTestFunctionsInFile(abs)
		if err != nil {
			return nil, err
		}
		allTests = append(allTests, tests...)
	}
	slices.Sort(allTests)
	return slices.Compact(allTests), nil
}

// windowsAdoptPlan describes how internal/adopt is replayed on Windows.
type windowsAdoptPlan struct {
	skipAdopt  bool
	wholeAdopt bool
	testNames  []string
	runPattern string
	skipReason string
	logMessage string
}

func hasNonTestFile(files []string) bool {
	for i := 0; i < len(files) && i < maxTestFiles; i++ {
		if !strings.HasSuffix(files[i], "_test.go") {
			return true
		}
	}
	return false
}

// planWindowsAdoptReplay computes whether internal/adopt should be replayed whole, filtered by Test
// functions, or skipped on Windows.
func planWindowsAdoptReplay(repoRoot string, replayFiles []string) (windowsAdoptPlan, error) {
	if len(replayFiles) == 0 {
		return windowsAdoptPlan{
			skipAdopt:  true,
			skipReason: adoptWindowsSkipReason,
			logMessage: "nested replay: windows-specific tests: none",
		}, nil
	}
	if hasNonTestFile(replayFiles) {
		return windowsAdoptPlan{
			wholeAdopt: true,
			logMessage: "nested replay: full",
		}, nil
	}
	tests, err := findTestFunctions(repoRoot, replayFiles)
	if err != nil {
		return windowsAdoptPlan{}, err
	}
	if len(tests) == 0 {
		return windowsAdoptPlan{
			skipAdopt:  true,
			skipReason: adoptWindowsSkipReason,
			logMessage: "nested replay: windows-specific tests: none",
		}, nil
	}
	return windowsAdoptPlan{
		testNames:  tests,
		runPattern: fmt.Sprintf("^(%s)$", strings.Join(tests, "|")),
		logMessage: fmt.Sprintf("nested replay: windows-specific tests: %s", strings.Join(tests, ", ")),
	}, nil
}

// TestWindowsAdoptReplayListMatchesActualFiles guards against Windows-specific files under
// internal/adopt being omitted from windowsAdoptReplayFiles. Runs on all platforms without build tags.
func TestWindowsAdoptReplayListMatchesActualFiles(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsAdoptReplay(repoRoot, windowsAdoptReplayFiles); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyWindowsAdoptReplay_PlantedAndStaleCases verifies comparison behavior on a temporary folder.
func TestVerifyWindowsAdoptReplay_PlantedAndStaleCases(t *testing.T) {
	t.Run("planted foo_windows.go reported missing", func(t *testing.T) {
		tmpRoot := t.TempDir()
		adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
		if err := os.MkdirAll(adoptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		testWrite(t, adoptDir, "foo_windows.go", "package adopt\n")
		err := verifyWindowsAdoptReplay(tmpRoot, nil)
		if err == nil || !strings.Contains(err.Error(), "foo_windows.go") || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("expected missing foo_windows.go error, got: %v", err)
		}
	})

	t.Run("planted build constraint reported missing", func(t *testing.T) {
		tmpRoot := t.TempDir()
		adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
		if err := os.MkdirAll(adoptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		testWrite(t, adoptDir, "bar_test.go", "//go:build windows && amd64\n\npackage adopt\n")
		err := verifyWindowsAdoptReplay(tmpRoot, nil)
		if err == nil || !strings.Contains(err.Error(), "bar_test.go") || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("expected missing bar_test.go error, got: %v", err)
		}
	})

	t.Run("stale list entry reported", func(t *testing.T) {
		tmpRoot := t.TempDir()
		adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
		if err := os.MkdirAll(adoptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		err := verifyWindowsAdoptReplay(tmpRoot, []string{"internal/adopt/stale_windows_test.go"})
		if err == nil || !strings.Contains(err.Error(), "stale_windows_test.go") || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected stale list entry error, got: %v", err)
		}
	})

	t.Run("clean folder passes", func(t *testing.T) {
		tmpRoot := t.TempDir()
		adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
		if err := os.MkdirAll(adoptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		testWrite(t, adoptDir, "normal.go", "package adopt\n")
		if err := verifyWindowsAdoptReplay(tmpRoot, nil); err != nil {
			t.Fatalf("clean folder must pass, got: %v", err)
		}
	})
}

// TestPlanWindowsAdoptReplay_Cases verifies all branches of planning for Windows adopt replay.
func TestPlanWindowsAdoptReplay_Cases(t *testing.T) {
	tmpRoot := t.TempDir()
	adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
	if err := os.MkdirAll(adoptDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("empty replay list skips adopt", func(t *testing.T) {
		plan, err := planWindowsAdoptReplay(tmpRoot, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.skipAdopt || plan.skipReason != adoptWindowsSkipReason || plan.logMessage != "nested replay: windows-specific tests: none" {
			t.Fatalf("unexpected plan for empty list: %+v", plan)
		}
	})

	t.Run("non-test file replays whole package", func(t *testing.T) {
		testWrite(t, adoptDir, "launcher_windows.go", "package adopt\n")
		plan, err := planWindowsAdoptReplay(tmpRoot, []string{"internal/adopt/launcher_windows.go"})
		if err != nil {
			t.Fatal(err)
		}
		if !plan.wholeAdopt || plan.logMessage != "nested replay: full" {
			t.Fatalf("unexpected plan for non-test file: %+v", plan)
		}
	})

	t.Run("test file with zero Test functions skips adopt", func(t *testing.T) {
		testWrite(t, adoptDir, "helper_windows_test.go", "package adopt\n\nfunc helper() {}\n")
		plan, err := planWindowsAdoptReplay(tmpRoot, []string{"internal/adopt/helper_windows_test.go"})
		if err != nil {
			t.Fatal(err)
		}
		if !plan.skipAdopt || plan.skipReason != adoptWindowsSkipReason || plan.logMessage != "nested replay: windows-specific tests: none" {
			t.Fatalf("unexpected plan for test file without tests: %+v", plan)
		}
	})

	t.Run("test file with Test functions builds run pattern", func(t *testing.T) {
		content := "package adopt\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\nfunc TestBeta(t *testing.T) {}\n"
		testWrite(t, adoptDir, "feature_windows_test.go", content)
		plan, err := planWindowsAdoptReplay(tmpRoot, []string{"internal/adopt/feature_windows_test.go"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.skipAdopt || plan.runPattern != "^(TestAlpha|TestBeta)$" {
			t.Fatalf("unexpected plan for test functions: %+v", plan)
		}
		if plan.logMessage != "nested replay: windows-specific tests: TestAlpha, TestBeta" {
			t.Fatalf("unexpected log message: %s", plan.logMessage)
		}
	})
}

// TestFindTestFunctions_EdgeCases verifies AST parsing handles methods, helpers and syntax errors.
func TestFindTestFunctions_EdgeCases(t *testing.T) {
	tmpRoot := t.TempDir()
	adoptDir := filepath.Join(tmpRoot, "internal", "adopt")
	if err := os.MkdirAll(adoptDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "package adopt\n\nimport \"testing\"\n\ntype S struct{}\nfunc (s *S) TestMethod(t *testing.T) {}\nfunc testHelper() {}\nfunc TestReal(t *testing.T) {}\n"
	testWrite(t, adoptDir, "edge_windows_test.go", content)
	tests, err := findTestFunctions(tmpRoot, []string{"internal/adopt/edge_windows_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TestReal"}; !slices.Equal(tests, want) {
		t.Fatalf("got tests %v, want %v", tests, want)
	}

	testWrite(t, adoptDir, "bad_windows_test.go", "package broken {{{{")
	if _, err := findTestFunctions(tmpRoot, []string{"internal/adopt/bad_windows_test.go"}); err == nil {
		t.Fatal("expected syntax error when parsing broken Go file")
	}
}

// TestNestedGoTestArgsWithRun_CarriesPattern verifies argument construction with and without -run.
func TestNestedGoTestArgsWithRun_CarriesPattern(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Minute))
	defer cancel()

	args, err := nestedGoTestArgsWithRun(ctx, time.Now(), "^(TestA|TestB)$", []string{"./internal/adopt/..."})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-run") || !slices.Contains(args, "^(TestA|TestB)$") {
		t.Fatalf("expected args to include -run pattern, got: %v", args)
	}

	argsNoRun, err := nestedGoTestArgsWithRun(ctx, time.Now(), "", []string{"./internal/adopt/..."})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(argsNoRun, "-run") {
		t.Fatalf("expected args without -run, got: %v", argsNoRun)
	}
}
