package dedupe_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

type exceptionScanCase struct {
	name        string
	native      bool
	foreign     bool
	expires     string
	wantNative  int
	wantForeign int
	wantStale   int
	wantPassed  bool
}

func TestForeignScan_ExceptionUsage(t *testing.T) {
	cases := []exceptionScanCase{
		{"live_both", true, true, "2026-11-18", 1, 1, 0, true},
		{"live_native_only", true, false, "2026-11-18", 1, 0, 0, true},
		{"live_foreign_only", false, true, "2026-11-18", 0, 1, 0, true},
		{"live_neither", false, false, "2026-11-18", 0, 0, 1, false},
		{"expiry_day_both", true, true, "2026-10-21", 1, 1, 0, true},
		{"expired_both", true, true, "2026-10-20", 0, 0, 0, false},
		{"expired_native_only", true, false, "2026-10-20", 0, 0, 0, false},
		{"expired_foreign_only", false, true, "2026-10-20", 0, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := scanExceptionFixture(t, tc)
			assertExceptionScanReport(t, report, tc)
		})
	}
}

func scanExceptionFixture(t *testing.T, tc exceptionScanCase) *dedupe.DedupeReport {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "clones.go", "package sample\n")
	writeFile(t, root, "app.py", "print('fixture')\n")
	path := "app.py"
	if tc.native {
		path = "clones.go"
		writeFile(t, root, path, deepCopyGeneratedClonesSource)
	}
	canned := `{"duplicates":[]}`
	if tc.foreign {
		canned = fmt.Sprintf(`{"duplicates":[{"format":"python","lines":10,"tokens":50,
		"firstFile":{"name":%q,"start":1,"end":10},
		"secondFile":{"name":%q,"start":30,"end":39}}]}`,
			filepath.Join(root, path), filepath.Join(root, path))
	}
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)
	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{
		RepoPath: root,
		Today:    time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		Exceptions: []config.Exception{{
			Rule: config.ExceptionRuleDedupe, Path: path,
			Reason: "Category D: retained fixture clones", Expires: tc.expires,
		}},
	})
	if err != nil {
		t.Fatalf("scan exception fixture: %v", err)
	}
	return report
}

func assertExceptionScanReport(t *testing.T, report *dedupe.DedupeReport, tc exceptionScanCase) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ForeignExcepted []dedupe.DuplicateGroup `json:"foreign_excepted"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if report.ForeignScanner != "jscpd "+dedupe.JscpdVersion || report.ForeignSkipped != "" {
		t.Fatalf("stub scanner not executed: %+v", report)
	}
	if len(report.Excepted) != tc.wantNative || len(wire.ForeignExcepted) != tc.wantForeign {
		t.Errorf("excepted native/foreign = %d/%d, want %d/%d", len(report.Excepted), len(wire.ForeignExcepted), tc.wantNative, tc.wantForeign)
	}
	if len(report.StaleExceptions) != tc.wantStale || report.Passed != tc.wantPassed {
		t.Errorf("stale/passed = %d/%v, want %d/%v", len(report.StaleExceptions), report.Passed, tc.wantStale, tc.wantPassed)
	}
	assertExceptionFindingCounts(t, report, tc)
	assertExceptionGroupMetadata(t, wire.ForeignExcepted, tc.expires, false)
	if tc.expires == "2026-10-20" {
		assertExceptionGroupMetadata(t, report.Duplicates, tc.expires, true)
		assertExceptionGroupMetadata(t, report.ForeignDuplicates, tc.expires, true)
	}
}

func assertExceptionFindingCounts(t *testing.T, report *dedupe.DedupeReport, tc exceptionScanCase) {
	t.Helper()
	wantNative, wantForeign := 0, 0
	if tc.native {
		wantNative = 1 - tc.wantNative
	}
	if tc.foreign {
		wantForeign = 1 - tc.wantForeign
	}
	if len(report.Duplicates) != wantNative || len(report.ForeignDuplicates) != wantForeign {
		t.Errorf("unexcused native/foreign = %d/%d, want %d/%d", len(report.Duplicates), len(report.ForeignDuplicates), wantNative, wantForeign)
	}
	if report.CleanlinessScore != float64(100-15*wantNative) {
		t.Errorf("score = %v, want %d", report.CleanlinessScore, 100-15*wantNative)
	}
}

func assertExceptionGroupMetadata(t *testing.T, groups []dedupe.DuplicateGroup, expires string, expired bool) {
	t.Helper()
	for _, group := range groups {
		if group.Language == "" && !expired {
			t.Error("foreign excepted group lost its language")
		}
		if !expired {
			if len(group.Exceptions) != 1 || group.Exceptions[0].Expires != expires {
				t.Errorf("exception metadata = %+v, want expiry %s", group.Exceptions, expires)
			}
			continue
		}
		for _, loc := range group.Locations {
			if loc.Expired != expires {
				t.Errorf("%s:%d expiry = %q, want %q", loc.Path, loc.Line, loc.Expired, expires)
			}
		}
	}
}

func TestForeignScan_ExceptionRootBoundary(t *testing.T) {
	for _, mainPath := range []string{"app.py", "other.py"} {
		t.Run(mainPath, func(t *testing.T) {
			root, other := t.TempDir(), t.TempDir()
			writeFile(t, root, "main.go", "package sample\n")
			writeFile(t, root, "app.py", "print('fixture')\n")
			writeFile(t, root, "other.py", "print('fixture')\n")
			writeFile(t, other, "app.py", "print('fixture')\n")
			canned := fmt.Sprintf(`{"duplicates":[{"format":"python","lines":10,"tokens":50,
			"firstFile":{"name":%q,"start":1,"end":10},
			"secondFile":{"name":%q,"start":1,"end":10}}]}`,
				filepath.Join(root, mainPath), filepath.Join(other, "app.py"))
			t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
			t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
			t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)
			report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{
				RepoPath: root, AcrossRoots: []string{other},
				Today: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
				Exceptions: []config.Exception{{Rule: config.ExceptionRuleDedupe, Path: "app.py",
					Reason: "Category D: main repository clones", Expires: "2026-11-18"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantStale := 0
			if mainPath == "other.py" {
				wantStale = 1
			}
			if len(report.ForeignDuplicates) != 1 || len(report.StaleExceptions) != wantStale {
				t.Errorf("foreign duplicates/stale = %d/%d, want 1/%d", len(report.ForeignDuplicates), len(report.StaleExceptions), wantStale)
			}
		})
	}
}

func TestForeignScan_Negative_ScannedForeignExceptionWithoutDuplicateIsStale(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package sample\n")
	writeFile(t, root, "interop.c", "int f(void) { return 0; }\n")
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", `{"duplicates":[]}`)
	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{
		RepoPath: root,
		Today:    time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		Exceptions: []config.Exception{{
			Rule:    config.ExceptionRuleDedupe,
			Path:    "interop.c",
			Reason:  "Category D: C interop",
			Expires: "2026-11-18",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignScanned == nil || report.ForeignScanned["c"] != 1 {
		t.Fatalf("expected ForeignScanned to have c=1, got %v", report.ForeignScanned)
	}
	if len(report.StaleExceptions) != 1 {
		t.Fatalf("expected 1 stale exception when C was scanned, got %d: %v", len(report.StaleExceptions), report.StaleExceptions)
	}
	want := "exceptions entry interop.c (HISS-19): excuses no duplicate function block; remove the entry"
	if report.StaleExceptions[0] != want {
		t.Fatalf("stale exception = %q, want %q", report.StaleExceptions[0], want)
	}
	if len(report.NotCheckedExceptions) != 0 {
		t.Fatalf("expected 0 not checked exceptions, got %d: %v", len(report.NotCheckedExceptions), report.NotCheckedExceptions)
	}
}

// TestMain lets this test binary act as jscpd (testsupport.RunFakeJscpdIfRequested): a test points
// PRAETOR_JSCPD_BIN at os.Args[0] and sets testsupport.FakeJscpdEnv.
func TestMain(m *testing.M) {
	if code, ran := testsupport.RunFakeJscpdIfRequested("jscpd " + dedupe.JscpdVersion); ran {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestJscpdVersionPin_MatchesPackageJSON(t *testing.T) {
	pkgPath := filepath.Join("..", "..", "tools", "jscpd", "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatalf("read tools/jscpd/package.json: %v", err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("parse tools/jscpd/package.json: %v", err)
	}
	got := pkg.Dependencies["jscpd"]
	if got != dedupe.JscpdVersion {
		t.Fatalf("tools/jscpd/package.json pinned jscpd %q, want JscpdVersion %q", got, dedupe.JscpdVersion)
	}
}

func setupCannedFixtures(t *testing.T, root1, root2 string) {
	t.Helper()
	writeFile(t, root1, "main.go", "package main\n")
	writeFile(t, root1, "main.c", "int f() { return 1; }\n")
	writeFile(t, root1, "util.c", "int f() { return 1; }\n")
	writeFile(t, root1, "app.py", "def run(): pass\n")
	writeFile(t, root2, "helper.py", "def run(): pass\n")
	writeFile(t, root1, "kernel.cu", "__global__ void k() {}\n")
	writeFile(t, root1, "compute.cu", "__global__ void k() {}\n")
	writeFile(t, root1, "src/lib.rs", "fn calc() {}\n")
	writeFile(t, root1, "src/algo.rs", "fn calc() {}\n")
	writeFile(t, root1, "scripts/run.sh", "echo run\n")
	writeFile(t, root1, "scripts/exec.sh", "echo run\n")
	writeFile(t, root1, ".github/workflows/ci.yml", "name: CI\n")
	writeFile(t, root2, ".github/workflows/cd.yml", "name: CD\n")
}

func buildCannedReport(root1, root2 string) string {
	return fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "c",
      "lines": 10,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 10},
      "secondFile": {"name": %q, "start": 1, "end": 10}
    },
    {
      "format": "python",
      "lines": 12,
      "tokens": 60,
      "firstFile": {"name": %q, "start": 5, "end": 16},
      "secondFile": {"name": %q, "start": 15, "end": 26}
    },
    {
      "format": "cpp",
      "lines": 15,
      "tokens": 70,
      "firstFile": {"name": %q, "start": 30, "end": 44},
      "secondFile": {"name": %q, "start": 40, "end": 54}
    },
    {
      "format": "rust",
      "lines": 8,
      "tokens": 55,
      "firstFile": {"name": %q, "start": 50, "end": 57},
      "secondFile": {"name": %q, "start": 60, "end": 67}
    },
    {
      "format": "bash",
      "lines": 6,
      "tokens": 30,
      "firstFile": {"name": %q, "start": 1, "end": 6},
      "secondFile": {"name": %q, "start": 1, "end": 6}
    },
    {
      "format": "yaml",
      "lines": 20,
      "tokens": 100,
      "firstFile": {"name": %q, "start": 10, "end": 29},
      "secondFile": {"name": %q, "start": 25, "end": 44}
    }
  ]
}`,
		filepath.Join(root1, "main.c"), filepath.Join(root1, "util.c"),
		filepath.Join(root1, "app.py"), filepath.Join(root2, "helper.py"),
		filepath.Join(root1, "kernel.cu"), filepath.Join(root1, "compute.cu"),
		filepath.Join(root1, "src", "lib.rs"), filepath.Join(root1, "src", "algo.rs"),
		filepath.Join(root1, "scripts", "run.sh"), filepath.Join(root1, "scripts", "exec.sh"),
		filepath.Join(root1, ".github", "workflows", "ci.yml"), filepath.Join(root2, ".github", "workflows", "cd.yml"),
	)
}

func checkLocation(t *testing.T, loc dedupe.FileLocation, wantRoot, wantPath string) {
	t.Helper()
	if loc.Root != wantRoot || loc.Path != wantPath {
		t.Fatalf("location = %+v, want Root=%s Path=%s", loc, wantRoot, wantPath)
	}
}

func verifySingleCannedGroup(t *testing.T, g dedupe.DuplicateGroup, lang string) {
	t.Helper()
	if len(g.Locations) != 2 || g.Hash == "" {
		t.Fatalf("invalid duplicate group for %q: %+v", lang, g)
	}
}

func verifyCannedReportGroups(t *testing.T, report *dedupe.DedupeReport, root1, root2 string) {
	t.Helper()
	byLang := make(map[string]dedupe.DuplicateGroup, len(report.ForeignDuplicates))
	for _, g := range report.ForeignDuplicates {
		byLang[g.Language] = g
	}

	for _, l := range []string{"c", "python", "cuda", "rust", "shell", "yaml"} {
		verifySingleCannedGroup(t, byLang[l], l)
	}

	pyGroup := byLang["python"]
	checkLocation(t, pyGroup.Locations[0], root1, "app.py")
	checkLocation(t, pyGroup.Locations[1], root2, "helper.py")

	yamlGroup := byLang["yaml"]
	checkLocation(t, yamlGroup.Locations[0], root1, ".github/workflows/ci.yml")
	checkLocation(t, yamlGroup.Locations[1], root2, ".github/workflows/cd.yml")
}

func TestForeignScan_Positive_CannedReport(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	setupCannedFixtures(t, root1, root2)
	canned := buildCannedReport(root1, root2)

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	opts := dedupe.ScanOptions{
		RepoPath:    root1,
		AcrossRoots: []string{root2},
	}
	report, err := dedupe.ScanRepoWithOptions(t.Context(), opts)
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}

	if want := "jscpd " + dedupe.JscpdVersion; report.ForeignScanner != want {
		t.Fatalf("ForeignScanner = %q, want %q", report.ForeignScanner, want)
	}
	if report.ForeignSkipped != "" {
		t.Fatalf("ForeignSkipped = %q, want empty", report.ForeignSkipped)
	}
	if len(report.ForeignDuplicates) != 6 {
		t.Fatalf("ForeignDuplicates count = %d, want 6", len(report.ForeignDuplicates))
	}

	verifyCannedReportGroups(t, report, root1, root2)

	if len(report.Unscanned) == 0 {
		t.Fatalf("Unscanned = %v, want non-empty (gate does not judge foreign sources)", report.Unscanned)
	}
	if !report.Partial {
		t.Fatalf("Partial = false, want true when foreign sources are present")
	}
	if len(report.ForeignScanned) < 6 {
		t.Fatalf("ForeignScanned = %v, want at least 6 languages", report.ForeignScanned)
	}
}

func TestForeignScan_Negative_BinaryNotFound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "print('hello')\n")

	t.Setenv("PRAETOR_JSCPD_BIN", filepath.Join(root, "definitely_nonexistent_jscpd"))

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan must succeed even when jscpd is unavailable, got err: %v", err)
	}
	if report.ForeignScanner != "" {
		t.Fatalf("ForeignScanner = %q, want empty", report.ForeignScanner)
	}
	if report.ForeignSkipped == "" {
		t.Fatalf("ForeignSkipped is empty, want explanation")
	}
	if report.Passed {
		t.Fatalf("Passed must be false when jscpd binary is missing")
	}
	if !strings.Contains(report.ForeignSkipped, "definitely_nonexistent_jscpd") {
		t.Fatalf("ForeignSkipped = %q, want binary path", report.ForeignSkipped)
	}
	if !strings.Contains(report.ForeignSkipped, dedupe.JscpdVersion) {
		t.Fatalf("ForeignSkipped = %q, want wanted version %q", report.ForeignSkipped, dedupe.JscpdVersion)
	}
	if report.Unscanned["python"] != 1 {
		t.Fatalf("Unscanned[python] = %d, want 1", report.Unscanned["python"])
	}
	if !report.Partial {
		t.Fatalf("Partial must be true when jscpd is skipped")
	}
	if len(report.ForeignDuplicates) != 0 {
		t.Fatalf("ForeignDuplicates = %v, want empty", report.ForeignDuplicates)
	}
}

func TestForeignScan_Negative_VersionMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "print('hello')\n")

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_VERSION", "jscpd 5.3.0")

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan must succeed with skipped foreign scanner: %v", err)
	}
	if report.ForeignScanner != "" {
		t.Fatalf("ForeignScanner = %q, want empty", report.ForeignScanner)
	}
	if report.ForeignSkipped == "" {
		t.Fatalf("ForeignSkipped must record version mismatch")
	}
	if report.Passed {
		t.Fatalf("Passed must be false on version mismatch")
	}
	if !strings.Contains(report.ForeignSkipped, `"jscpd 5.3.0"`) || !strings.Contains(report.ForeignSkipped, `"jscpd 5.4.2"`) {
		t.Fatalf("ForeignSkipped = %q, want both found and wanted versions quoted", report.ForeignSkipped)
	}
	if report.Unscanned["python"] != 1 {
		t.Fatalf("Unscanned[python] = %d, want 1", report.Unscanned["python"])
	}
	if !report.Partial {
		t.Fatalf("Partial must be true")
	}
}

func TestForeignScan_Negative_ReportOverByteBound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "print('hello')\n")

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_OVERSIZED", "1")

	_, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err == nil {
		t.Fatalf("expected error when report exceeds byte bound, got nil")
	}
}

func TestForeignScan_Positive_ThreeCopyFragmentMerged(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "a.py", "def fn(): pass\n")
	writeFile(t, root, "b.py", "def fn(): pass\n")
	writeFile(t, root, "c.py", "def fn(): pass\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 10,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 10},
      "secondFile": {"name": %q, "start": 1, "end": 10}
    },
    {
      "format": "python",
      "lines": 10,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 10},
      "secondFile": {"name": %q, "start": 1, "end": 10}
    }
  ]
}`,
		filepath.Join(root, "a.py"), filepath.Join(root, "b.py"),
		filepath.Join(root, "a.py"), filepath.Join(root, "c.py"),
	)

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1 merged group", len(report.ForeignDuplicates))
	}
	group := report.ForeignDuplicates[0]
	if len(group.Locations) != 3 {
		t.Fatalf("group.Locations count = %d, want 3", len(group.Locations))
	}
	if group.Locations[0].Path != "a.py" || group.Locations[1].Path != "b.py" || group.Locations[2].Path != "c.py" {
		t.Fatalf("group.Locations paths = %+v, want [a.py, b.py, c.py]", group.Locations)
	}
}

func TestForeignScan_Boundary_GoOnlyRepoRunsNoJscpd(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")

	t.Setenv("PRAETOR_JSCPD_BIN", "/nonexistent/jscpd")

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if report.ForeignScanner != "" {
		t.Fatalf("ForeignScanner = %q, want empty for Go-only repo", report.ForeignScanner)
	}
	if report.ForeignSkipped != "" {
		t.Fatalf("ForeignSkipped = %q, want empty for Go-only repo", report.ForeignSkipped)
	}
	if len(report.ForeignDuplicates) != 0 {
		t.Fatalf("ForeignDuplicates = %v, want empty", report.ForeignDuplicates)
	}
}

func TestForeignScan_Boundary_AcrossWithMainRootOnlyBehavesLikeNoAcross(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "a.py", "print('a')\n")
	writeFile(t, root, "b.py", "print('b')\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 5,
      "tokens": 20,
      "firstFile": {"name": %q, "start": 1, "end": 5},
      "secondFile": {"name": %q, "start": 1, "end": 5}
    }
  ]
}`, filepath.Join(root, "a.py"), filepath.Join(root, "b.py"))

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{
		RepoPath:    root,
		AcrossRoots: []string{root},
	})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	locs := report.ForeignDuplicates[0].Locations
	for _, loc := range locs {
		if loc.Root != "" {
			t.Fatalf("loc.Root = %q, want empty when across is main root only", loc.Root)
		}
	}
}

func TestForeignScan_Boundary_SelfPairYieldsNoGroup(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	sveltePath := filepath.Join("apps", "docs", "src", "routes", "+page.svelte")
	writeFile(t, root, sveltePath, "<h1>docs</h1>\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "html",
      "lines": 119,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 119},
      "secondFile": {"name": %q, "start": 1, "end": 119}
    }
  ]
}`, filepath.Join(root, sveltePath), filepath.Join(root, sveltePath))

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 0 {
		t.Fatalf("ForeignDuplicates count = %d, want 0", len(report.ForeignDuplicates))
	}
}

func TestForeignScan_Boundary_SameFileDifferentLinesYieldsTwoLocations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "def run(): pass\ndef exec(): pass\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 10,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 10},
      "secondFile": {"name": %q, "start": 20, "end": 30}
    }
  ]
}`, filepath.Join(root, "app.py"), filepath.Join(root, "app.py"))

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	group := report.ForeignDuplicates[0]
	if len(group.Locations) != 2 {
		t.Fatalf("group.Locations count = %d, want 2", len(group.Locations))
	}
	if group.Locations[0].Path != "app.py" || group.Locations[1].Path != "app.py" {
		t.Fatalf("group.Locations paths = %+v, want [app.py, app.py]", group.Locations)
	}
	if group.Locations[0].Line != 1 || group.Locations[1].Line != 20 {
		t.Fatalf("group.Locations lines = [%d, %d], want [1, 20]", group.Locations[0].Line, group.Locations[1].Line)
	}
}

func TestForeignScan_Positive_ContiguousCloneSpanMergedToOneCopy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "a/trivy_scan.py", "# scan a\n")
	writeFile(t, root, "b/trivy_scan.py", "# scan b\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 163, "end": 173},
      "secondFile": {"name": %q, "start": 212, "end": 221}
    },
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 173, "end": 183},
      "secondFile": {"name": %q, "start": 222, "end": 232}
    }
  ]
}`,
		filepath.Join(root, "a", "trivy_scan.py"), filepath.Join(root, "b", "trivy_scan.py"),
		filepath.Join(root, "a", "trivy_scan.py"), filepath.Join(root, "b", "trivy_scan.py"),
	)

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	group := report.ForeignDuplicates[0]
	if len(group.Locations) != 2 {
		t.Fatalf("group.Locations count = %d, want 2", len(group.Locations))
	}
	if group.LOC != 21 {
		t.Fatalf("group.LOC = %d, want 21", group.LOC)
	}
	if group.Locations[0].Path != "a/trivy_scan.py" || group.Locations[0].Line != 163 {
		t.Fatalf("group.Locations[0] = %+v, want a/trivy_scan.py:163", group.Locations[0])
	}
	if group.Locations[1].Path != "b/trivy_scan.py" || group.Locations[1].Line != 212 {
		t.Fatalf("group.Locations[1] = %+v, want b/trivy_scan.py:212", group.Locations[1])
	}
}

type realRepeatCase struct {
	name      string
	canned    func(root string) string
	wantCount int
	wantLOC   int
	wantLines []int
}

func runRealRepeatCase(t *testing.T, tc realRepeatCase) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "# python fixture\n")

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", tc.canned(root))

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	group := report.ForeignDuplicates[0]
	if len(group.Locations) != tc.wantCount {
		t.Fatalf("group.Locations count = %d, want %d", len(group.Locations), tc.wantCount)
	}
	if group.LOC != tc.wantLOC {
		t.Fatalf("group.LOC = %d, want %d", group.LOC, tc.wantLOC)
	}
	for i, wantLine := range tc.wantLines {
		if group.Locations[i].Line != wantLine {
			t.Fatalf("group.Locations[%d].Line = %d, want %d", i, group.Locations[i].Line, wantLine)
		}
	}
}

func realRepeatCases() []realRepeatCase {
	return []realRepeatCase{
		{
			name: "far_apart",
			canned: func(root string) string {
				path := filepath.Join(root, "app.py")
				return fmt.Sprintf(`{"duplicates":[{"format":"python","lines":11,"tokens":50,"firstFile":{"name":%q,"start":10,"end":20},"secondFile":{"name":%q,"start":50,"end":60}}]}`, path, path)
			},
			wantCount: 2,
			wantLOC:   11,
			wantLines: []int{10, 50},
		},
		{
			name: "directly_adjacent",
			canned: func(root string) string {
				path := filepath.Join(root, "app.py")
				return fmt.Sprintf(`{"duplicates":[{"format":"python","lines":10,"tokens":50,"firstFile":{"name":%q,"start":10,"end":19},"secondFile":{"name":%q,"start":20,"end":29}}]}`, path, path)
			},
			wantCount: 2,
			wantLOC:   10,
			wantLines: []int{10, 20},
		},
		{
			name: "chain_three_adjacent",
			canned: func(root string) string {
				path := filepath.Join(root, "app.py")
				return fmt.Sprintf(`{"duplicates":[
					{"format":"python","lines":10,"tokens":50,"firstFile":{"name":%q,"start":20,"end":29},"secondFile":{"name":%q,"start":10,"end":19}},
					{"format":"python","lines":10,"tokens":50,"firstFile":{"name":%q,"start":20,"end":29},"secondFile":{"name":%q,"start":30,"end":39}}
				]}`, path, path, path, path)
			},
			wantCount: 3,
			wantLOC:   10,
			wantLines: []int{10, 20, 30},
		},
		{
			name: "overlapping",
			canned: func(root string) string {
				path := filepath.Join(root, "app.py")
				return fmt.Sprintf(`{"duplicates":[{"format":"python","lines":16,"tokens":50,"firstFile":{"name":%q,"start":10,"end":25},"secondFile":{"name":%q,"start":20,"end":35}}]}`, path, path)
			},
			wantCount: 2,
			wantLOC:   16,
			wantLines: []int{10, 20},
		},
	}
}

func TestForeignScan_Negative_RealRepeatsStay(t *testing.T) {
	for _, tc := range realRepeatCases() {
		t.Run(tc.name, func(t *testing.T) {
			runRealRepeatCase(t, tc)
		})
	}
}

// 100-110 and 110-120 are one split block; 121-131 abuts it but is the real repeat of its second
// half, so it stays a copy of its own.
func TestForeignScan_Negative_SplitBlockBesideItsOwnRepeat(t *testing.T) {
	runRealRepeatCase(t, realRepeatCase{
		canned: func(root string) string {
			path := filepath.Join(root, "app.py")
			return fmt.Sprintf(`{"duplicates":[
				{"format":"python","lines":11,"tokens":50,"firstFile":{"name":%q,"start":100,"end":110},"secondFile":{"name":%q,"start":200,"end":210}},
				{"format":"python","lines":11,"tokens":50,"firstFile":{"name":%q,"start":110,"end":120},"secondFile":{"name":%q,"start":121,"end":131}}
			]}`, path, path, path, path)
		},
		wantCount: 3,
		wantLOC:   21,
		wantLines: []int{100, 121, 200},
	})
}

func TestForeignScan_Boundary_GapOfOneLineYieldsTwoGroups(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "a.py", "# a\n")
	writeFile(t, root, "b.py", "# b\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 10, "end": 20},
      "secondFile": {"name": %q, "start": 10, "end": 20}
    },
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 22, "end": 32},
      "secondFile": {"name": %q, "start": 22, "end": 32}
    }
  ]
}`,
		filepath.Join(root, "a.py"), filepath.Join(root, "b.py"),
		filepath.Join(root, "a.py"), filepath.Join(root, "b.py"),
	)

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 2 {
		t.Fatalf("ForeignDuplicates count = %d, want 2 groups for gap of one line", len(report.ForeignDuplicates))
	}
	for i, g := range report.ForeignDuplicates {
		if len(g.Locations) != 2 {
			t.Fatalf("group[%d].Locations count = %d, want 2", i, len(g.Locations))
		}
	}
}

func TestForeignScan_Boundary_EndAbsentZeroMergesByStartPlusLinesMinusOne(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "a.py", "# a\n")
	writeFile(t, root, "b.py", "# b\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 163, "end": 0},
      "secondFile": {"name": %q, "start": 212, "end": 0}
    },
    {
      "format": "python",
      "lines": 11,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 173, "end": 0},
      "secondFile": {"name": %q, "start": 222, "end": 0}
    }
  ]
}`,
		filepath.Join(root, "a.py"), filepath.Join(root, "b.py"),
		filepath.Join(root, "a.py"), filepath.Join(root, "b.py"),
	)

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	group := report.ForeignDuplicates[0]
	if len(group.Locations) != 2 {
		t.Fatalf("group.Locations count = %d, want 2", len(group.Locations))
	}
	if group.LOC != 21 {
		t.Fatalf("group.LOC = %d, want 21", group.LOC)
	}
	if group.Locations[0].Line != 163 || group.Locations[1].Line != 212 {
		t.Fatalf("group.Locations lines = [%d, %d], want [163, 212]", group.Locations[0].Line, group.Locations[1].Line)
	}
}

func TestForeignScan_Boundary_UnmergedGroupHashUnchanged(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, "app.py", "# app\n")

	canned := fmt.Sprintf(`{
  "duplicates": [
    {
      "format": "python",
      "lines": 10,
      "tokens": 50,
      "firstFile": {"name": %q, "start": 1, "end": 10},
      "secondFile": {"name": %q, "start": 20, "end": 30}
    }
  ]
}`, filepath.Join(root, "app.py"), filepath.Join(root, "app.py"))

	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", canned)

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("ScanRepoWithOptions: %v", err)
	}
	if len(report.ForeignDuplicates) != 1 {
		t.Fatalf("ForeignDuplicates count = %d, want 1", len(report.ForeignDuplicates))
	}
	const wantHash = "a2adde83d939121bff0b49228da1125239bd9f0150ff18dc4ebc137c88ebb821"
	if got := report.ForeignDuplicates[0].Hash; got != wantHash {
		t.Fatalf("group.Hash = %q, want fixed baseline %q", got, wantHash)
	}
}

const (
	realCFixture = `
int compute_complex_math(int alpha, int beta, int gamma, int delta) {
    int result = alpha * 2 + beta * 3;
    result += gamma * 4 - delta * 5;
    result = result * (alpha + beta) / (gamma + delta + 1);
    result = (result << 2) ^ (alpha >> 1);
    result += (beta << 3) ^ (gamma >> 2);
    return result;
}
`
	realPyFixture = `
def process_pipeline_records(record_list, batch_size, threshold_limit):
    processed = []
    for item in record_list:
        val = item.get('metric', 0) * 2.5
        if val > threshold_limit and len(processed) < batch_size:
            processed.append({'id': item['id'], 'score': val, 'valid': True})
    return processed
`
	realCuFixture = `
__global__ void compute_cuda_matrix(float *output_data, const float *input_data, int width, int height) {
    int col = blockIdx.x * blockDim.x + threadIdx.x;
    int row = blockIdx.y * blockDim.y + threadIdx.y;
    if (col < width && row < height) {
        int index = row * width + col;
        float value = input_data[index] * 2.5f + 1.0f;
        output_data[index] = value * value;
    }
}
`
	realYamlFixture = `
name: Polyglot Integration Test Workflow
on:
  push:
    branches:
      - main
      - develop
jobs:
  matrix_build_verify:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node_version: [18, 20, 22]
    steps:
      - uses: actions/checkout@v4
      - name: Setup Node environment
        uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: npm ci
      - run: npm test
`
)

func setupRealBinaryFixtures(t *testing.T, root1, root2 string) {
	t.Helper()
	writeFile(t, root1, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, root1, "func1.c", realCFixture)
	writeFile(t, root1, "func2.c", realCFixture)
	writeFile(t, root1, "calc.py", realPyFixture)
	writeFile(t, root2, "calc_copy.py", realPyFixture)
	writeFile(t, root1, "algo1.cu", realCuFixture)
	writeFile(t, root1, "algo2.cu", realCuFixture)
	writeFile(t, root1, ".github/workflows/ci.yml", realYamlFixture)
	writeFile(t, root2, ".github/workflows/ci_copy.yml", realYamlFixture)
}

func verifyRealBinaryReport(t *testing.T, report *dedupe.DedupeReport) {
	t.Helper()
	if report.ForeignScanner == "" {
		t.Fatalf("ForeignScanner is empty, skipped: %s", report.ForeignSkipped)
	}
	if len(report.ForeignDuplicates) < 4 {
		t.Fatalf("expected at least 4 foreign duplicate groups with real jscpd, got %d", len(report.ForeignDuplicates))
	}
	byLang := make(map[string]dedupe.DuplicateGroup)
	for _, g := range report.ForeignDuplicates {
		byLang[g.Language] = g
	}
	for _, expectedLang := range []string{"c", "python", "cuda", "yaml"} {
		if _, ok := byLang[expectedLang]; !ok {
			t.Fatalf("real jscpd scan missing duplicate group for %q, got: %+v", expectedLang, report.ForeignDuplicates)
		}
	}
}

func TestForeignScan_Integration_RealBinary(t *testing.T) {
	bin := os.Getenv("PRAETOR_JSCPD_BIN")
	if bin == "" {
		t.Skip("skipping real jscpd integration test: PRAETOR_JSCPD_BIN not set")
	}

	root1 := t.TempDir()
	root2 := t.TempDir()
	setupRealBinaryFixtures(t, root1, root2)

	opts := dedupe.ScanOptions{
		RepoPath:    root1,
		AcrossRoots: []string{root2},
	}
	report, err := dedupe.ScanRepoWithOptions(t.Context(), opts)
	if err != nil {
		t.Fatalf("real jscpd scan failed: %v", err)
	}
	verifyRealBinaryReport(t, report)
}
