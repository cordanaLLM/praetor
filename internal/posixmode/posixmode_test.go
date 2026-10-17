// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package posixmode

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// allowedModeChecks are the reviewed comparisons of a permission mode with a literal in files
// every platform builds, by slash path and function, with why each holds on every platform.
var allowedModeChecks = map[string]string{
	"internal/adopt/hook_runner_audit.go:hookRunnable": "tests the owner-execute bit only after the goos == windows branch, " +
		"which judges the #! line instead",
	"internal/editor/capabilities.go:isExecutableRegularFile": "tests the execute bits only after the goos == windows branch, " +
		"which judges the file extension instead",
	"internal/dogfood/schedule_io.go:checkScheduleWriteTarget": "tests the owner-write bit, which Windows expresses " +
		"(0444 for a read-only file, 0666 otherwise); privacy is judged by util.ArtefactPrivacy",
}

// parseFixture parses src as the file name.
func parseFixture(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return fset, file
}

// plantedRefusal is internal/dogfood/schedule_transcript_store.go:61 before #132 moved it into a
// Linux-only file: it refuses on every host whose modes are not POSIX.
const plantedRefusal = `package dogfood

import "os"

func openTranscriptDir(info os.FileInfo, private bool) error {
	if !info.IsDir() || !private || info.Mode().Perm() != 0o700 {
		return os.ErrPermission
	}
	return nil
}
`

// tightening records and tightens a mode and decides nothing on its value (util.tightenMode).
const tightening = `package util

import "os"

func tightenMode(info os.FileInfo, perm os.FileMode, chmod func(os.FileMode) error) error {
	target := info.Mode().Perm() & perm
	if target == info.Mode().Perm() {
		return nil
	}
	return chmod(target)
}
`

// The matcher (#1374). Positive: the pre-#132 refusal and an executable-bit test in an untagged
// file are found, by line and function. Negative: a tightening comparison with a variable is
// not. Boundary: the same refusal in a _linux.go file, a //go:build !windows file and a test file
// is in no file every platform builds.
func TestComparisons_3D(t *testing.T) {
	fset, file := parseFixture(t, "store.go", plantedRefusal)
	got := Comparisons(fset, file)
	if !Applies("internal/dogfood/store.go", file) || len(got) != 1 || got[0].Pos.Line != 6 || got[0].Func != "openTranscriptDir" {
		t.Errorf("planted pre-#132 refusal: %+v", got)
	}
	masked := strings.Replace(plantedRefusal, "info.Mode().Perm() != 0o700", "(info.Mode().Perm())&0o100 == 0", 1)
	fset, file = parseFixture(t, "store.go", masked)
	if got := Comparisons(fset, file); len(got) != 1 {
		t.Errorf("masked execute-bit test: %+v", got)
	}
	method := strings.Replace(plantedRefusal, "func openTranscriptDir(", "func (s *store) openTranscriptDir(", 1)
	fset, file = parseFixture(t, "store.go", method)
	if got := Comparisons(fset, file); len(got) != 1 || got[0].Func != "store.openTranscriptDir" {
		t.Errorf("method: %+v", got)
	}

	fset, file = parseFixture(t, "secure.go", tightening)
	if got := Comparisons(fset, file); len(got) != 0 {
		t.Errorf("tightening comparison: %+v", got)
	}

	for name, src := range map[string]string{
		"internal/dogfood/store_linux.go":       plantedRefusal,
		"internal/dogfood/store_linux_amd64.go": plantedRefusal,
		"internal/dogfood/store.go":             "//go:build !windows\n\n" + plantedRefusal,
		"internal/dogfood/store_test.go":        plantedRefusal,
	} {
		_, file := parseFixture(t, filepath.Base(name), src)
		if Applies(name, file) {
			t.Errorf("%s is read as a file every platform builds", name)
		}
	}
	_, file = parseFixture(t, "linux_paths.go", plantedRefusal)
	if !Applies("internal/dogfood/linux_paths.go", file) {
		t.Error("a name that only starts with a GOOS is untagged")
	}
}

// The mask walk is a bounded loop (HISS-01, HISS-02). Positive: a Perm() call under two literal
// masks, the literal on either side, is a mode. Negative: a mask by a variable is not. Boundary:
// maxMaskDepth masks are unwrapped, one more is not.
func TestIsModeWalksNestedMasks(t *testing.T) {
	nested := func(depth int) string {
		expr := "info.Mode().Perm()"
		for i := 0; i < depth; i++ {
			expr = "(" + expr + " & 0o777)"
		}
		return expr
	}
	cases := []struct {
		name, expr string
		want       bool
	}{
		{"two masks", "0o700 &^ (info.Mode().Perm() & 0o777)", true},
		{"variable mask", "info.Mode().Perm() & perm", false},
		{"at the bound", nested(maxMaskDepth), true},
		{"past the bound", nested(maxMaskDepth + 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got := isMode(expr); got != tc.want {
				t.Errorf("isMode(%s) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// The engine's own tree compares no permission mode with a literal in a file every platform
// builds, except the reviewed uses allowedModeChecks names, and every entry still names one.
func TestRepositoryHasNoUntaggedPosixModeComparison(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string]bool{}
	var failures []string
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name := entry.Name(); file != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		src, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		fset, parsed := parseFixture(t, rel, string(src))
		if !Applies(rel, parsed) {
			return nil
		}
		for _, finding := range Comparisons(fset, parsed) {
			key := rel + ":" + finding.Func
			if _, allowed := allowedModeChecks[key]; allowed {
				used[key] = true
				continue
			}
			failures = append(failures, rel+":"+strconv.Itoa(finding.Pos.Line)+" ("+finding.Func+") compares a permission mode with a literal "+
				"in a file every platform builds; move the check into a build-tagged file (_linux.go, //go:build !windows), "+
				"or add "+key+" to allowedModeChecks with why it holds on Windows")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range allowedModeChecks {
		if !used[key] {
			failures = append(failures, "allowedModeChecks entry "+key+" names no comparison any more; remove it")
		}
	}
	sort.Strings(failures)
	for _, failure := range slices.Compact(failures) {
		t.Error(failure)
	}
}
