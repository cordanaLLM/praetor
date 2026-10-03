package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteWithReportIn is WriteWithReport under the caller's context, confined to root (#717).

// symlinkOrSkip creates link pointing at target, or skips the test where the platform refuses
// an unprivileged symlink (Windows without developer mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
}

// Positive: an existing JSON file keeps its key and gains the managed one, an absent file is
// created from its template, and the report names each outcome in set order.
func TestWriteWithReportIn_Positive_MergesAndCreates(t *testing.T) {
	root := writeFixture(t, map[string]string{".vscode/settings.json": `{"human":true}`})
	set := &EditorConfigSet{Files: []GeneratedFile{
		{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`},
		{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: `{"version":"2.0.0"}` + "\n"},
	}}
	report, err := WriteWithReportIn(context.Background(), set, root)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(report.Files) != 2 || report.Files[0].Outcome != WriteMerged || report.Files[1].Outcome != WriteCreated {
		t.Fatalf("report = %+v, want MERGED then CREATED", report.Files)
	}
	merged := mustRead(t, filepath.Join(root, ".vscode", "settings.json"))
	if !strings.Contains(merged, `"human": true`) || !strings.Contains(merged, `"managed": 1`) {
		t.Fatalf("merge lost a key:\n%s", merged)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "tasks.json")); got != set.Files[1].Content {
		t.Fatalf("created file = %q, want the template", got)
	}
}

// Negative: a refused file (a commented .vscode file lacking a managed value) aborts the run
// before any write, a symlinked directory below root is refused instead of followed, and a
// missing context or a cancelled one writes nothing.
func TestWriteWithReportIn_Negative_RefusesBeforeAnyWrite(t *testing.T) {
	const commented = "{\n  // adopter note\n  \"human\": true\n}\n"
	root := writeFixture(t, map[string]string{".vscode/settings.json": commented})
	set := &EditorConfigSet{Files: []GeneratedFile{
		{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: "{}\n"},
		{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`},
	}}
	_, err := WriteWithReportIn(context.Background(), set, root)
	var refusal *CommentedJSONError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), ".vscode/settings.json") {
		t.Fatalf("refusal = %v, want a CommentedJSONError naming the file", err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); got != commented {
		t.Fatalf("refused file rewritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".vscode", "tasks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file earlier in the set was written before the refusal: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	empty := t.TempDir()
	//nolint:staticcheck // SA1012: a nil context is the input under test.
	if _, err := WriteWithReportIn(nil, set, empty); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WriteWithReportIn(cancelled, set, empty); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %v, want context.Canceled", err)
	}
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("a refused context wrote %v (%v)", entries, err)
	}
}

// Negative: a directory below root that links outside it is refused, and nothing lands outside.
func TestWriteWithReportIn_Negative_RefusesLinkedDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(root, ".vscode"))
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`}}}
	if _, err := WriteWithReportIn(context.Background(), set, root); err == nil {
		t.Fatal("a linked directory below root was followed")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside root: %v (%v)", entries, err)
	}
}

// Boundary: a file already holding every managed value keeps its bytes and is PRESENT, a
// developer-owned file the confined read refuses (a link) is PRESERVED and left alone, and an
// empty set writes nothing.
func TestWriteWithReportIn_Boundary_PresentPreservedAndEmpty(t *testing.T) {
	const complete = "{\n  // adopter note\n  \"managed\": 1,\n}\n"
	root := writeFixture(t, map[string]string{".vscode/settings.json": complete})
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`}}}
	report, err := WriteWithReportIn(context.Background(), set, root)
	if err != nil || len(report.Files) != 1 || report.Files[0].Outcome != WritePresent {
		t.Fatalf("complete file = %+v, %v; want PRESENT", report, err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); got != complete {
		t.Fatalf("a complete file was rewritten:\n%s", got)
	}

	report, err = WriteWithReportIn(context.Background(), &EditorConfigSet{}, t.TempDir())
	if err != nil || len(report.Files) != 0 {
		t.Fatalf("empty set = %+v, %v; want no outcome and no error", report, err)
	}

	own := filepath.Join(t.TempDir(), "own.lua")
	if err := os.WriteFile(own, []byte("vim.opt.number = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	symlinkOrSkip(t, own, filepath.Join(linked, ".nvim.lua"))
	preserved := &EditorConfigSet{Files: []GeneratedFile{{Path: ".nvim.lua", Editor: EditorNeovim, Content: "-- template\n"}}}
	report, err = WriteWithReportIn(context.Background(), preserved, linked)
	if err != nil || len(report.Files) != 1 || report.Files[0].Outcome != WritePreserved {
		t.Fatalf("linked developer-owned file = %+v, %v; want PRESERVED", report, err)
	}
	if got := mustRead(t, own); got != "vim.opt.number = true\n" {
		t.Fatalf("a preserved file's target changed: %q", got)
	}
}
