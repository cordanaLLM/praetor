package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// strictAdopterFuncLOC is the function length limit the public dogfood suite adopts with
// (internal/dogfood/public_policy_test.go). Adoption copies the checkpoint scripts into the
// adopter's tree, and the ratchet requires every touched file to be clean, so one vendored
// function over the adopter's limit fails the adoption itself.
const strictAdopterFuncLOC = 35

// pythonFunction returns a module holding one function of exactly lines lines.
func pythonFunction(lines int) string {
	return "def count():\n    value = 0\n" + strings.Repeat("    value += 1\n", lines-3) + "    return value\n"
}

// shellFunction returns a script holding one function of exactly lines lines, header to brace.
func shellFunction(lines int) string {
	return "#!/bin/sh\nset -eu\ncount() {\n" + strings.Repeat("  : step\n", lines-2) + "}\ncount\n"
}

// scanStrict scans files, written at their relative paths under a fresh root, with the
// strict adopter limit and returns every violation it reports.
func scanStrict(t *testing.T, files map[string]string) []hiss.InvariantViolation {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
	report, err := hiss.Scan(t.Context(), root, hiss.ScanOptions{MaxFuncLOC: strictAdopterFuncLOC})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Incomplete() {
		t.Fatalf("scan did not cover the vendored sources: %+v", report.Skips)
	}
	return report.Violations
}

// TestVendoredCheckpointSourcesFitStrictAdopterLOC keeps the scripts adoption writes clean
// under the strictest limit the public dogfood suite adopts with: the checkpoint scripts it
// vendors, the evasion interceptor and the engine launcher it renders. Black formatting once
// stretched six of the checkpoint functions past it, and every public adoption under that
// policy failed on files Praetor itself had written; the launcher's install_pinned later did
// the same at 37 lines.
func TestVendoredCheckpointSourcesFitStrictAdopterLOC(t *testing.T) {
	engineRoot := filepath.Join("..", "..")
	files := map[string]string{evasionHookFile: buildBlockEvasionPY(), engineLauncherFile: engineLauncherScript}
	for _, name := range []string{checkpointScript, checkpointCommon} {
		body, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read vendored source %s: %v", name, err)
		}
		files[name] = string(body)
	}
	for _, violation := range scanStrict(t, files) {
		t.Errorf("[%s] %s:%d %s", violation.RuleID, violation.FilePath, violation.LineNumber, violation.Message)
	}
}

// TestStrictAdopterLOCGuardBoundary proves the guard above can fail, at each kind of path it
// scans: a function one line over the limit is reported, and one exactly at it is not.
func TestStrictAdopterLOCGuardBoundary(t *testing.T) {
	for _, test := range []struct {
		path        string
		lines, want int
		render      func(int) string
	}{
		{checkpointScript, strictAdopterFuncLOC + 1, 1, pythonFunction}, {checkpointScript, strictAdopterFuncLOC, 0, pythonFunction},
		{evasionHookFile, strictAdopterFuncLOC + 1, 1, pythonFunction}, {evasionHookFile, strictAdopterFuncLOC, 0, pythonFunction},
		{engineLauncherFile, strictAdopterFuncLOC + 1, 1, shellFunction}, {engineLauncherFile, strictAdopterFuncLOC, 0, shellFunction},
	} {
		t.Run(fmt.Sprintf("%s-%d-lines", filepath.Base(test.path), test.lines), func(t *testing.T) {
			got := scanStrict(t, map[string]string{test.path: test.render(test.lines)})
			if len(got) != test.want {
				t.Fatalf("%d-line function: got %d violations, want %d: %+v", test.lines, len(got), test.want, got)
			}
			if test.want == 1 && got[0].RuleID != "HISS-04" {
				t.Fatalf("over-limit function reported under %s, want HISS-04", got[0].RuleID)
			}
		})
	}
}
