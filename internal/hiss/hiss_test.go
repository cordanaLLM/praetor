package hiss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldIgnorePath(t *testing.T) {
	tests := []struct {
		path   string
		ignore bool
	}{
		{"vendor/foo/bar.go", true},
		{"node_modules/pkg/index.js", true},
		{".git/config", true},
		{"src/main.go", false},
		{"internal/util/util.go", false},
		{"core/build/output.o", true},
	}

	for _, tt := range tests {
		got := ShouldIgnorePath(tt.path)
		if got != tt.ignore {
			t.Errorf("ShouldIgnorePath(%q) = %v, want %v", tt.path, got, tt.ignore)
		}
	}
}

func setupHissTestFixtures(t *testing.T, tempDir string) {
	t.Helper()
	pyCode := `
def recursive_func(n):
    if n <= 0:
        return 0
    return recursive_func(n - 1)

def loop_func():
    while True:
        pass

def error_func():
    try:
        x = 1
    except:
        pass
`
	if err := os.WriteFile(filepath.Join(tempDir, "test.py"), []byte(pyCode), 0644); err != nil {
		t.Fatal(err)
	}

	goCode := "package test\n\nfunc infinite() {\n\t" + "for" + " {\n\t\twork()\n\t}\n}\n\nfunc panicky() {\n\t" + "pan" + "ic(\"crash\")\n}\n"
	if err := os.WriteFile(filepath.Join(tempDir, "test.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	rsCode := `
fn do_something() {
    let opt = Some(1);
    let val = opt.unwrap();
    unsafe {
        println!("unsafe");
    }
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "test.rs"), []byte(rsCode), 0644); err != nil {
		t.Fatal(err)
	}

	cCode := "#include <string.h>\n\nvoid allocate(char *dst, const char *src) {\n    start:\n    strcpy(dst, src);\n    " + "go" + "to start;\n}\n"
	if err := os.WriteFile(filepath.Join(tempDir, "test.c"), []byte(cCode), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestHissScanRules(t *testing.T) {
	tempDir := t.TempDir()
	setupHissTestFixtures(t, tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report, err := Scan(ctx, tempDir, ScanOptions{MaxFuncLOC: 60, Cap: 100})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if report.TotalInfractions == 0 {
		t.Fatalf("Expected violations, got 0")
	}

	foundRules := make(map[string]bool)
	for _, v := range report.Violations {
		foundRules[v.RuleID] = true
	}

	expectedRules := []string{"HISS-01", "HISS-02", "HISS-07", "HISS-09"}
	for _, r := range expectedRules {
		if !foundRules[r] {
			t.Errorf("Expected rule %s to be flagged, but was not found. Report breakdown: %+v", r, report.Breakdown)
		}
	}

	// Test ConvertToBaseline
	baselineInfractions := ConvertToBaseline(report.Violations)
	if len(baselineInfractions) != len(report.Violations) {
		t.Errorf("ConvertToBaseline count mismatch: got %d, want %d", len(baselineInfractions), len(report.Violations))
	}
}

func TestHissScanLongFunction(t *testing.T) {
	tempDir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("package test\n\nfunc veryLongFunc() {\n")
	for i := 0; i < 70; i++ {
		sb.WriteString("\tprintln(\"line\")\n")
	}
	sb.WriteString("}\n")

	if err := os.WriteFile(filepath.Join(tempDir, "long.go"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	report, err := Scan(ctx, tempDir, ScanOptions{MaxFuncLOC: 60})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	foundLOC := false
	for _, v := range report.Violations {
		if v.RuleID == "HISS-04" {
			foundLOC = true
			break
		}
	}
	if !foundLOC {
		t.Errorf("Expected HISS-04 violation for >60 LOC function, but none found")
	}
}
