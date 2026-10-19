package dedupe_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
)

func TestMain(m *testing.M) {
	if os.Getenv("PRAETOR_TEST_FAKE_JSCPD") == "1" {
		runFakeJscpd()
		return
	}
	os.Exit(m.Run())
}

func runFakeJscpd() {
	args := os.Args[1:]
	if handleFakeJscpdVersion(args) {
		return
	}
	cfgPath := findFakeJscpdConfig(args)
	outDir := parseFakeJscpdOutDir(cfgPath)
	reportPath := filepath.Join(outDir, "jscpd-report.json")
	if writeFakeJscpdOversizedReport(reportPath) {
		return
	}
	writeFakeJscpdCannedReport(reportPath)
}

func handleFakeJscpdVersion(args []string) bool {
	if len(args) == 0 || args[0] != "--version" {
		return false
	}
	v := os.Getenv("PRAETOR_TEST_FAKE_JSCPD_VERSION")
	if v == "" {
		v = "jscpd " + dedupe.JscpdVersion
	}
	fmt.Println(v)
	os.Exit(0)
	return true
}

func findFakeJscpdConfig(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			return args[i+1]
		}
	}
	fmt.Fprintln(os.Stderr, "fake jscpd: missing -c flag")
	os.Exit(1)
	return ""
}

func parseFakeJscpdOutDir(cfgPath string) string {
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake jscpd: read config %s: %v\n", cfgPath, err)
		os.Exit(1)
	}
	var cfg struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "fake jscpd: parse config %s: %v\n", cfgPath, err)
		os.Exit(1)
	}
	return cfg.Output
}

func writeFakeJscpdOversizedReport(reportPath string) bool {
	if os.Getenv("PRAETOR_TEST_FAKE_JSCPD_OVERSIZED") != "1" {
		return false
	}
	oversized := make([]byte, 17<<20)
	for i := range oversized {
		oversized[i] = ' '
	}
	if err := os.WriteFile(reportPath, oversized, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "fake jscpd: write oversized report: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

func writeFakeJscpdCannedReport(reportPath string) {
	cannedContent := os.Getenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT")
	if cannedContent == "" {
		cannedContent = `{"duplicates":[]}`
	}
	if err := os.WriteFile(reportPath, []byte(cannedContent), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "fake jscpd: write report: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
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

	if report.ForeignScanner != "jscpd 5.4.1" {
		t.Fatalf("ForeignScanner = %q, want %q", report.ForeignScanner, "jscpd 5.4.1")
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
