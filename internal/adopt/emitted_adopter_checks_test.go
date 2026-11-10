// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/docstrings"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// maxEmittedWalkEntries bounds directory traversal across the emitted adopter tree (HISS-02).
// Current emitted tree measures 54 walked entries.
const maxEmittedWalkEntries = 512

// maxEmittedFileBytes bounds the size of any single emitted file read (HISS-02).
// Current emitted tree measures 140,214 bytes for the largest file (verify.mjs).
const maxEmittedFileBytes = 512 * 1024

// maxEmittedTotalBytes bounds the cumulative bytes read across emitted files (HISS-02).
// Current emitted tree measures 643,439 bytes across scanned source files (746,426 bytes across all emitted files).
const maxEmittedTotalBytes = 2 * 1024 * 1024

// vendoredDocstringExceptionReason preserves pinned upstream bytes until their documentation changes.
const vendoredDocstringExceptionReason = "category D: vendored interfig upstream source (tools/figures/third_party/interfig/VENDOR.md); upstream bytes stay unedited, so this undocumented function stays until an upstream update documents it"

// vendoredDocstringExceptions names only measured undocumented functions in pinned upstream source.
var vendoredDocstringExceptions = []config.Exception{
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/geometry.ts", Function: "route",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/geometry.ts", Function: "other",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/model.ts", Function: "toBeat",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/svg.ts", Function: "nodeWidth",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/svg.ts", Function: "size",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/svg.ts", Function: "place",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/svg.ts", Function: "toSvg",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
	{
		Rule: "docstrings", Path: "tools/figures/third_party/interfig/upstream/src/svg.ts", Function: "minH",
		Category: "D", Entered: "2026-11-10", Expires: "2027-03-10", Reason: vendoredDocstringExceptionReason,
	},
}

// emittedAdopterTree writes the same renderings pinned by the emitted fixture generator.
func emittedAdopterTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range emittedHookRenderings(t) {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	assets, err := managedasset.Assets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(asset.Path)), string(asset.Data))
	}
	for _, rel := range checkpointBundle {
		text := mustRead(t, filepath.Join("..", "..", filepath.FromSlash(rel)))
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	return root
}

// emittedPythonFindings applies the Pelorus rules through Python's AST and comment tokenizer.
func emittedPythonFindings(t *testing.T, root string) ([]string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; emitted Python adopter rules cannot run on this platform")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := util.RunCommand(ctx, "", python, "-B", "testdata/emitted_python_checks.py", root)
	if err != nil {
		return nil, fmt.Errorf("emitted Python checks: %w\n%s", err, out)
	}
	var findings []string
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		return nil, fmt.Errorf("decode emitted Python findings: %w\n%s", err, out)
	}
	return findings, nil
}

// TestEmittedAdopterPythonRules checks suppressions and empty handlers across the emitted tree.
func TestEmittedAdopterPythonRules(t *testing.T) {
	findings, err := emittedPythonFindings(t, emittedAdopterTree(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestEmittedAdopterPythonRulesPlanted proves each rule rejects its defect in an emitted copy.
func TestEmittedAdopterPythonRulesPlanted(t *testing.T) {
	for _, fixture := range []struct{ name, text, want string }{
		{"reason", "value = 1  # nosemgrep: example.rule\n", "nosemgrep needs"},
		{"start", "# nosemgrep: example.rule - explanation\nvalue = 1\n", "start line"},
		{"except", "try:\n    value = 1\nexcept:\n    pass\n", "empty except"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := emittedAdopterTree(t)
			mustWrite(t, filepath.Join(root, "planted.py"), fixture.text)
			findings, err := emittedPythonFindings(t, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || !strings.Contains(findings[0], fixture.want) {
				t.Fatalf("findings=%v; want the planted %s defect", findings, fixture.name)
			}
		})
	}
}

// TestEmittedAdopterPythonRulesBoundary accepts explained handlers and ignores markers in strings.
func TestEmittedAdopterPythonRulesBoundary(t *testing.T) {
	root := t.TempDir()
	text := "value = (  # nosemgrep: first.rule, second.rule - explanation\n    1\n)\n" +
		"try:\n    value = 1\nexcept OSError:\n    # Missing optional state is empty.\n    pass\n" +
		"text = 'nosemgrep'\n"
	mustWrite(t, filepath.Join(root, "boundary.py"), text)
	findings, err := emittedPythonFindings(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("valid boundary rejected: %v", findings)
	}
}

// emittedDocstringScan walks one emitted tree within the entry and byte bounds (HISS-02).
type emittedDocstringScan struct {
	root       string
	visited    int
	totalBytes int64
	findings   []string
}

// readSource reads one emitted source file within the per-file and total byte bounds.
func (s *emittedDocstringScan) readSource(path, rel string) ([]byte, error) {
	data, err := util.ReadFileLimited(path, maxEmittedFileBytes)
	if errors.Is(err, util.ErrFileTooLarge) {
		return nil, fmt.Errorf("emitted file %s exceeds %d bytes: %w", rel, maxEmittedFileBytes, err)
	}
	if err != nil {
		return nil, fmt.Errorf("read emitted file %s: %w", rel, err)
	}
	s.totalBytes += int64(len(data))
	if s.totalBytes > maxEmittedTotalBytes {
		return nil, fmt.Errorf("emitted total read exceeds %d bytes", maxEmittedTotalBytes)
	}
	return data, nil
}

// checkSource records every undocumented function of one emitted source file.
func (s *emittedDocstringScan) checkSource(path string) error {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return err
	}
	data, err := s.readSource(path, rel)
	if err != nil {
		return err
	}
	functions, err := hiss.ExtractFileFunctions(rel, data)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, fn := range functions {
		if !docstrings.HasDocstring(rel, data, lines, fn) {
			s.findings = append(s.findings, fmt.Sprintf("%s:%d: undocumented %s", rel, fn.Start, fn.Name))
		}
	}
	return nil
}

// visit counts one walked entry against the entry bound and checks it when it is source.
func (s *emittedDocstringScan) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	s.visited++
	if s.visited > maxEmittedWalkEntries {
		return fmt.Errorf("emitted walk exceeds %d entries", maxEmittedWalkEntries)
	}
	switch filepath.Ext(path) {
	case ".py", ".js", ".mjs", ".cjs", ".ts", ".go":
		if !entry.IsDir() {
			return s.checkSource(path)
		}
	}
	return nil
}

// emittedDocstringFindings checks every Python, JavaScript, TypeScript and Go function, without diff exclusions.
func emittedDocstringFindings(root string) ([]string, error) {
	scan := &emittedDocstringScan{root: root}
	err := filepath.WalkDir(root, scan.visit)
	return scan.findings, err
}

// unexcusedDocstringFindings applies live path/function exceptions and reports invalid or stale entries.
func unexcusedDocstringFindings(findings []string, exceptions []config.Exception, today time.Time) (unexcused, problems []string) {
	keys := make([][2]string, len(findings))
	excused := make(map[[2]string]bool, len(findings))
	for i, finding := range findings {
		location, name, _ := strings.Cut(finding, ": undocumented ")
		path, _, _ := strings.Cut(location, ":")
		keys[i] = [2]string{filepath.ToSlash(path), name}
		excused[keys[i]] = false
	}
	for _, entry := range exceptions {
		label := fmt.Sprintf("%s: undocumented %s", entry.Path, entry.Function)
		if problem := rules.CategoryExpiryProblem(entry.Category, entry.Entered, entry.Expires, today); problem != "" {
			problems = append(problems, fmt.Sprintf("%s: %s", label, problem))
			continue
		}
		if entry.Expired(today) {
			problems = append(problems, label+": expired exception")
			continue
		}
		key := [2]string{entry.Path, entry.Function}
		if _, found := excused[key]; !found {
			problems = append(problems, label+": stale exception")
			continue
		}
		excused[key] = true
	}
	for i, finding := range findings {
		if !excused[keys[i]] {
			unexcused = append(unexcused, finding)
		}
	}
	return unexcused, problems
}

// TestEmittedAdopterDocstrings requires complete coverage of the adoption renderings.
func TestEmittedAdopterDocstrings(t *testing.T) {
	findings, err := emittedDocstringFindings(emittedAdopterTree(t))
	if err != nil {
		t.Fatal(err)
	}
	unexcused, problems := unexcusedDocstringFindings(findings, vendoredDocstringExceptions, time.Now())
	for _, finding := range append(unexcused, problems...) {
		t.Error(finding)
	}
}

// TestEmittedAdopterDocstringsPlanted rejects an undocumented function added to each emitted language.
func TestEmittedAdopterDocstringsPlanted(t *testing.T) {
	for _, fixture := range []struct{ path, text string }{
		{"planted.py", "def planted():\n    pass\n"},
		{"planted.mjs", "function planted() {}\n"},
		{"planted.ts", "function planted() {}\n"},
		{"planted.go", "package planted\nfunc planted() {}\n"},
	} {
		t.Run(fixture.path, func(t *testing.T) {
			root := emittedAdopterTree(t)
			mustWrite(t, filepath.Join(root, fixture.path), fixture.text)
			findings, err := emittedDocstringFindings(root)
			unexcused, problems := unexcusedDocstringFindings(findings, vendoredDocstringExceptions, time.Now())
			for _, problem := range problems {
				t.Error(problem)
			}
			if err != nil || len(unexcused) != 1 || !strings.Contains(unexcused[0], "undocumented planted") {
				t.Fatalf("findings=%v err=%v; want the planted undocumented function", unexcused, err)
			}
		})
	}
	t.Run("vendored_svg", func(t *testing.T) {
		root := emittedAdopterTree(t)
		rel := "tools/figures/third_party/interfig/upstream/src/svg.ts"
		path := filepath.Join(root, filepath.FromSlash(rel))
		mustWrite(t, path, mustRead(t, path)+"\nfunction planted() {}\n")
		findings, err := emittedDocstringFindings(root)
		unexcused, problems := unexcusedDocstringFindings(findings, vendoredDocstringExceptions, time.Now())
		for _, problem := range problems {
			t.Error(problem)
		}
		if err != nil || len(unexcused) != 1 || !strings.HasPrefix(filepath.ToSlash(unexcused[0]), rel+":") ||
			!strings.HasSuffix(unexcused[0], ": undocumented planted") {
			t.Fatalf("findings=%v err=%v; want only the planted vendored function", unexcused, err)
		}
	})
}

// TestEmittedAdopterDocstringExceptionsNegative rejects stale, expired and over-cap entries.
func TestEmittedAdopterDocstringExceptionsNegative(t *testing.T) {
	today := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	base := config.Exception{
		Rule: "docstrings", Path: "vendor.ts", Function: "known", Category: "D",
		Entered: "2026-11-01", Expires: "2027-02-01", Reason: "category D: test fixture",
	}
	for _, fixture := range []struct {
		name, path, function, expires, problem string
		extra                                  bool
		wantUnexcused                          int
	}{
		{"stale", "vendor.ts", "absent", "2027-02-01", "stale", true, 0},
		{"wrong_path", "other.ts", "known", "2027-02-01", "stale", false, 1},
		{"expired", "vendor.ts", "known", "2026-11-09", "expired", false, 1},
		{"cap", "vendor.ts", "known", "2027-03-02", "category D", false, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			entry := base
			entry.Path, entry.Function, entry.Expires = fixture.path, fixture.function, fixture.expires
			entries := []config.Exception{entry}
			if fixture.extra {
				entries = append(entries, base)
			}
			unexcused, problems := unexcusedDocstringFindings([]string{"vendor.ts:999: undocumented known"}, entries, today)
			if len(unexcused) != fixture.wantUnexcused {
				t.Errorf("unexcused=%v; want %d findings", unexcused, fixture.wantUnexcused)
			}
			if len(problems) != 1 {
				t.Fatalf("problems=%v; want one %s problem", problems, fixture.problem)
			}
			want := fmt.Sprintf("%s: undocumented %s", fixture.path, fixture.function)
			if !strings.Contains(problems[0], want) || !strings.Contains(problems[0], fixture.problem) {
				t.Errorf("problem=%q; want %q and %q", problems[0], want, fixture.problem)
			}
		})
	}
}

// TestEmittedAdopterDocstringExceptionsBoundary accepts the expiry day and the category D cap.
func TestEmittedAdopterDocstringExceptionsBoundary(t *testing.T) {
	today := time.Date(2026, time.November, 10, 23, 59, 59, 0, time.UTC)
	for _, fixture := range []struct{ name, expires string }{
		{"expiry_day", "2026-11-10"},
		{"120_days", "2027-03-10"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			entries := []config.Exception{{
				Rule: "docstrings", Path: "vendor.ts", Function: "known", Category: "D",
				Entered: "2026-11-10", Expires: fixture.expires, Reason: "category D: test fixture",
			}}
			unexcused, problems := unexcusedDocstringFindings([]string{"vendor.ts:999: undocumented known"}, entries, today)
			if len(unexcused) != 0 || len(problems) != 0 {
				t.Fatalf("valid boundary rejected: unexcused=%v problems=%v", unexcused, problems)
			}
		})
	}
}

// TestEmittedAdopterWalkEntriesBoundary accepts a tree at the entry bound and rejects one entry over.
func TestEmittedAdopterWalkEntriesBoundary(t *testing.T) {
	exactRoot := t.TempDir()
	for i := 0; i < maxEmittedWalkEntries-1; i++ {
		mustWrite(t, filepath.Join(exactRoot, fmt.Sprintf("file_%03d.txt", i)), "")
	}
	findings, err := emittedDocstringFindings(exactRoot)
	if err != nil {
		t.Fatalf("tree at bound %d entries failed: %v", maxEmittedWalkEntries, err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %v", findings)
	}

	overRoot := t.TempDir()
	for i := 0; i < maxEmittedWalkEntries; i++ {
		mustWrite(t, filepath.Join(overRoot, fmt.Sprintf("file_%03d.txt", i)), "")
	}
	_, err = emittedDocstringFindings(overRoot)
	if err == nil {
		t.Fatalf("tree at %d entries unexpectedly passed, want bound error", maxEmittedWalkEntries+1)
	}
	wantBound := fmt.Sprintf("%d", maxEmittedWalkEntries)
	if !strings.Contains(err.Error(), wantBound) {
		t.Fatalf("error %q does not name entry bound %s", err, wantBound)
	}
}

// TestEmittedAdopterFileBytesBoundary rejects a file one byte over the per-file read limit.
func TestEmittedAdopterFileBytesBoundary(t *testing.T) {
	root := t.TempDir()
	oversized := make([]byte, maxEmittedFileBytes+1)
	mustWrite(t, filepath.Join(root, "oversized.go"), string(oversized))
	_, err := emittedDocstringFindings(root)
	if err == nil {
		t.Fatalf("file at %d bytes unexpectedly passed, want bound error", maxEmittedFileBytes+1)
	}
	wantBound := fmt.Sprintf("%d", maxEmittedFileBytes)
	if !strings.Contains(err.Error(), wantBound) {
		t.Fatalf("error %q does not name file byte bound %s", err, wantBound)
	}
}

// TestEmittedAdopterTotalBytesBoundary rejects cumulative reads exceeding the total byte limit.
func TestEmittedAdopterTotalBytesBoundary(t *testing.T) {
	root := t.TempDir()
	// 4 files of maxEmittedFileBytes reach maxEmittedTotalBytes (2 MiB).
	for i := 0; i < 4; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("part%d.py", i)), strings.Repeat("#\n", maxEmittedFileBytes/2))
	}
	// 5th file pushes cumulative bytes over maxEmittedTotalBytes.
	mustWrite(t, filepath.Join(root, "overflow.py"), "#\n")
	_, err := emittedDocstringFindings(root)
	if err == nil {
		t.Fatalf("cumulative reads exceeding %d bytes passed, want bound error", maxEmittedTotalBytes)
	}
	wantBound := fmt.Sprintf("%d", maxEmittedTotalBytes)
	if !strings.Contains(err.Error(), wantBound) {
		t.Fatalf("error %q does not name total byte bound %s", err, wantBound)
	}
}

// writeEmptyFiles writes n empty files into a fresh temporary tree and returns its root.
func writeEmptyFiles(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("file_%03d.txt", i)), "")
	}
	return root
}

// requireBoundError fails unless err is a refusal that names bound.
func requireBoundError(t *testing.T, findings []string, err error, bound int) {
	t.Helper()
	if err == nil {
		t.Fatalf("over-bound tree passed with findings %v, want an error naming %d", findings, bound)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", bound)) {
		t.Fatalf("error %q does not name the bound %d", err, bound)
	}
}

// TestEmittedAdopterPythonBounds proves Python checks stop and fail on over-bound trees (HISS-02).
func TestEmittedAdopterPythonBounds(t *testing.T) {
	t.Run("file_bytes", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "oversized.py"), strings.Repeat("\n", maxEmittedFileBytes+1))
		findings, err := emittedPythonFindings(t, root)
		requireBoundError(t, findings, err, maxEmittedFileBytes)
	})
	t.Run("walk_entries", func(t *testing.T) {
		findings, err := emittedPythonFindings(t, writeEmptyFiles(t, maxEmittedWalkEntries))
		requireBoundError(t, findings, err, maxEmittedWalkEntries)
	})
	t.Run("walk_entries_boundary", func(t *testing.T) {
		findings, err := emittedPythonFindings(t, writeEmptyFiles(t, maxEmittedWalkEntries-1))
		if err != nil || len(findings) != 0 {
			t.Fatalf("tree at the %d-entry bound: findings %v, err %v", maxEmittedWalkEntries, findings, err)
		}
	})
}
