package dedupe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const pyDupBlock = `def sync_generated_block():
    print("step one in duplicated foreign block")
    print("step two in duplicated foreign block")
    print("step three in duplicated foreign block")
    print("step four in duplicated foreign block")
    return len("payload")
`

const goDupNoHeader = `package sample

import "fmt"

func DupHelper(items []string) int {
	fmt.Println("dup step one")
	fmt.Println("dup step two")
	fmt.Println("dup step three")
	fmt.Println("dup step four")
	return len(items)
}
`

// writeGitRepo commits paths, and a .gitattributes holding attrs when attrs is not empty, into a new repository at root.
func writeGitRepo(t *testing.T, root string, attrs string, paths map[string]string) {
	t.Helper()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	if attrs != "" {
		writeFile(t, root, ".gitattributes", attrs)
	}
	for name, body := range paths {
		writeFile(t, root, name, body)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "."}, []string{"commit", "-m", "fixture"})
}

// useFakeJscpdDupPair points the scan at the fake jscpd, answering one python clone pair per entry of pairs.
func useFakeJscpdDupPair(t *testing.T, root string, pairs [][2]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"duplicates":[`)
	for i, pair := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"format":"python","lines":10,"tokens":50,`+
			`"firstFile":{"name":%s,"start":1,"end":10},`+
			`"secondFile":{"name":%s,"start":1,"end":10}}`,
			jsonString(filepath.Join(root, pair[0])), jsonString(filepath.Join(root, pair[1])))
	}
	b.WriteString(`]}`)
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD", "1")
	t.Setenv("PRAETOR_JSCPD_BIN", os.Args[0])
	t.Setenv("PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT", b.String())
}

// jsonString quotes s as a JSON string for the canned fake jscpd report.
func jsonString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// TestScanRepo_Positive_LinguistGeneratedPythonSkippedFromForeign checks a linguist-generated python pair raises no
// foreign group while a hand-written pair beside it still does.
func TestScanRepo_Positive_LinguistGeneratedPythonSkippedFromForeign(t *testing.T) {
	root := t.TempDir()
	writeGitRepo(t, root, "sync_*.py linguist-generated=true\n",
		map[string]string{
			"sync_a.py": pyDupBlock,
			"sync_b.py": pyDupBlock,
			"hand_a.py": pyDupBlock,
			"hand_b.py": pyDupBlock,
		})
	useFakeJscpdDupPair(t, root, [][2]string{{"sync_a.py", "sync_b.py"}, {"hand_a.py", "hand_b.py"}})

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.SkippedGeneratedFiles < 2 {
		t.Errorf("SkippedGeneratedFiles = %d, want >= 2", report.SkippedGeneratedFiles)
	}
	if report.ForeignScanned["python"] != 2 || report.Unscanned["python"] != 4 {
		t.Errorf("foreign/inventory counts = %v/%v, want python=2/4", report.ForeignScanned, report.Unscanned)
	}
	for _, g := range report.ForeignDuplicates {
		for _, loc := range g.Locations {
			if strings.HasPrefix(loc.Path, "sync_") {
				t.Fatalf("foreign duplicate includes linguist-generated %q", loc.Path)
			}
		}
	}
	if len(report.ForeignDuplicates) == 0 {
		t.Fatal("expected foreign duplicate for non-generated hand copies")
	}
}

// TestScanRepo_Negative_LinguistUnsetAndFalseStillScanned checks an unset or false linguist-generated value skips nothing.
func TestScanRepo_Negative_LinguistUnsetAndFalseStillScanned(t *testing.T) {
	root := t.TempDir()
	writeGitRepo(t, root, "neg.py -linguist-generated\nfalse.py linguist-generated=false\n",
		map[string]string{
			"neg.py":    pyDupBlock,
			"neg2.py":   pyDupBlock,
			"false.py":  pyDupBlock,
			"false2.py": pyDupBlock,
		})
	useFakeJscpdDupPair(t, root, [][2]string{{"neg.py", "neg2.py"}, {"false.py", "false2.py"}})

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.SkippedGeneratedFiles != 0 {
		t.Errorf("SkippedGeneratedFiles = %d, want 0", report.SkippedGeneratedFiles)
	}
}

// TestScanRepo_Positive_LinguistGeneratedGoSkippedByNative checks the native scanner skips Go files git marks
// linguist-generated even without the cmd/go generated header.
func TestScanRepo_Positive_LinguistGeneratedGoSkippedByNative(t *testing.T) {
	root := t.TempDir()
	writeGitRepo(t, root, "gen_*.go linguist-generated=true\n",
		map[string]string{
			"gen_a.go": goDupNoHeader,
			"gen_b.go": goDupNoHeader,
		})

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.SkippedGeneratedFiles != 2 {
		t.Errorf("SkippedGeneratedFiles = %d, want 2", report.SkippedGeneratedFiles)
	}
	if len(report.Duplicates) != 0 {
		t.Fatalf("Duplicates = %d, want 0", len(report.Duplicates))
	}
}

// TestScanRepo_Boundary_NonGitDirectoryUsesHeaderOnly checks a directory outside git keeps the header rule only.
func TestScanRepo_Boundary_NonGitDirectoryUsesHeaderOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sync_a.py", pyDupBlock)
	writeFile(t, root, "sync_b.py", pyDupBlock)
	useFakeJscpdDupPair(t, root, [][2]string{{"sync_a.py", "sync_b.py"}})

	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.SkippedGeneratedFiles != 0 {
		t.Errorf("SkippedGeneratedFiles = %d, want 0 without git attributes", report.SkippedGeneratedFiles)
	}
}

// TestScanRepo_Negative_LinguistProbeFailureFailsScan checks a failing attribute probe fails the scan, naming the
// root and the attribute, instead of scanning generated files.
func TestScanRepo_Negative_LinguistProbeFailureFailsScan(t *testing.T) {
	root := t.TempDir()
	writeGitRepo(t, root, "*.py linguist-generated=true\n", map[string]string{"a.py": pyDupBlock})
	dedupe.SetLinguistGitProbeForTest(t, func(_ context.Context, dir string, _ int, _ ...string) (util.CommandBytes, error) {
		return util.CommandBytes{}, fmt.Errorf("stub linguist attribute probe in %s", dir)
	})

	_, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root})
	if err == nil {
		t.Fatal("expected scan failure when git cannot read attributes")
	}
	if !strings.Contains(err.Error(), root) {
		t.Fatalf("error = %q, want scan root %q named", err, root)
	}
	if !strings.Contains(err.Error(), "linguist-generated") {
		t.Fatalf("error = %q, want linguist-generated probe named", err)
	}
}

// TestSourceLanguageCounts_AttributeFailure keeps inventory counts independent of attribute classification.
func TestSourceLanguageCounts_AttributeFailure(t *testing.T) {
	root := t.TempDir()
	writeGitRepo(t, root, "*.py linguist-generated=true\n", map[string]string{"a.py": pyDupBlock, "a.go": "package sample\n"})
	dedupe.SetLinguistGitProbeForTest(t, func(_ context.Context, _ string, _ int, _ ...string) (util.CommandBytes, error) {
		return util.CommandBytes{}, fmt.Errorf("attribute lookup failed")
	})
	counts, err := dedupe.SourceLanguageCounts(t.Context(), root)
	if err != nil || counts["python"] != 1 || counts["go"] != 1 || len(counts) != 2 {
		t.Fatalf("counts/error = %v/%v, want go=1 python=1/nil", counts, err)
	}
}

// TestForeignScanned_AcrossGeneratedAndAbsolutePaths counts the exact jscpd inventory across roots.
func TestForeignScanned_AcrossGeneratedAndAbsolutePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "testdata", "repo")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	writeGitRepo(t, root, "gen.py linguist-generated=true\n", map[string]string{
		"gen.py": pyDupBlock, "hand.py": pyDupBlock, ".github/workflows/build.yml": "name: build\n",
	})
	writeGitRepo(t, extra, "gen.py linguist-generated=true\n", map[string]string{
		"gen.py": pyDupBlock, "hand.py": pyDupBlock, "script.sh": "echo fixture\n",
	})
	useFakeJscpdDupPair(t, root, nil)
	report, err := dedupe.ScanRepoWithOptions(t.Context(), dedupe.ScanOptions{RepoPath: root, AcrossRoots: []string{extra}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignScanned["python"] != 2 || report.ForeignScanned["yaml"] != 1 || report.ForeignScanned["shell"] != 1 {
		t.Fatalf("scanned = %v, want python=2 yaml=1 shell=1", report.ForeignScanned)
	}
	if report.SkippedGeneratedFiles != 2 {
		t.Fatalf("skipped = %d, want 2", report.SkippedGeneratedFiles)
	}
}
