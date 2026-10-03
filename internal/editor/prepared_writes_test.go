package editor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PrepareWritesIn resolves an editor set without writing; PreparedWrites.Publish writes it bound
// to the bytes, or the absence, each file was resolved from (#717).

// Positive: an untouched prepared set publishes the outcomes it predicted, merging and creating.
func TestPreparedWrites_Positive_PublishesPredictedOutcomes(t *testing.T) {
	root := writeFixture(t, map[string]string{".vscode/settings.json": `{"human":true}`})
	set := &EditorConfigSet{Files: []GeneratedFile{
		{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`},
		{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: "{}\n"},
	}}
	prepared, err := PrepareWritesIn(context.Background(), set, root)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".vscode", "tasks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepare wrote a file: %v", err)
	}
	predicted := prepared.Results()
	report, err := prepared.Publish(context.Background())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(predicted) != 2 || predicted[0].Outcome != WriteMerged || predicted[1].Outcome != WriteCreated ||
		fmt.Sprint(report.Files) != fmt.Sprint(predicted) {
		t.Fatalf("predicted %+v, published %+v; want MERGED then CREATED in both", predicted, report.Files)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); !strings.Contains(got, `"human": true`) ||
		!strings.Contains(got, `"managed": 1`) {
		t.Fatalf("merge lost a key:\n%s", got)
	}
}

// Negative: a file edited after it was resolved is refused at publish, naming it, and keeps the
// edit instead of receiving content merged from the earlier read; a nil context is refused.
func TestPreparedWrites_Negative_RefusesFileEditedAfterPrepare(t *testing.T) {
	root := writeFixture(t, map[string]string{".vscode/settings.json": `{"human":true}`})
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/settings.json", Editor: EditorVSCode, Content: `{"managed":1}`}}}
	prepared, err := PrepareWritesIn(context.Background(), set, root)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	const edited = `{"human":false,"added":"concurrently"}`
	writeTestFile(t, root, ".vscode/settings.json", edited)
	report, err := prepared.Publish(context.Background())
	if err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("edited file published: %+v, %v", report, err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "settings.json")); got != edited {
		t.Fatalf("an edit made after prepare was overwritten:\n%s", got)
	}
	if len(report.Files) != 0 {
		t.Errorf("a refused publish reported outcomes: %+v", report.Files)
	}
	//nolint:staticcheck // SA1012: a nil context is the input under test.
	if _, err := prepared.Publish(nil); err == nil {
		t.Error("nil context accepted")
	}
}

// Boundary: a file absent when resolved and created before publish is refused too, and the
// created file keeps its bytes.
func TestPreparedWrites_Boundary_RefusesFileCreatedAfterPrepare(t *testing.T) {
	root := t.TempDir()
	set := &EditorConfigSet{Files: []GeneratedFile{{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: "{}\n"}}}
	prepared, err := PrepareWritesIn(context.Background(), set, root)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if outcome := prepared.Results()[0].Outcome; outcome != WriteCreated {
		t.Fatalf("absent file predicted %q, want %q", outcome, WriteCreated)
	}
	const created = `{"version":"concurrent"}`
	writeTestFile(t, root, ".vscode/tasks.json", created)
	if _, err := prepared.Publish(context.Background()); err == nil || !strings.Contains(err.Error(), "tasks.json") {
		t.Fatalf("a file created after prepare was replaced: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, ".vscode", "tasks.json")); got != created {
		t.Fatalf("a file created after prepare was overwritten:\n%s", got)
	}
}

// Negative: under a cancelled context a set holding only developer-owned files, one present and
// one absent, fails with the cancellation instead of reporting both preserved, and writes nothing.
func TestPrepareWritesIn_Negative_CancelledContextPreservedFiles(t *testing.T) {
	root := writeFixture(t, map[string]string{".nvim.lua": "vim.opt.number = true\n"})
	set := &EditorConfigSet{Files: []GeneratedFile{
		{Path: ".nvim.lua", Editor: EditorNeovim, Content: "-- template\n"},
		{Path: ".editorconfig", Editor: EditorUniversal, Content: "root = true\n"},
	}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := writeIn(cancelled, set, root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run = %+v, %v; want context.Canceled", report, err)
	}
	if len(report.Files) != 0 {
		t.Errorf("a cancelled run reported outcomes: %+v", report.Files)
	}
	if _, err := os.Stat(filepath.Join(root, ".editorconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a cancelled run wrote .editorconfig: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, ".nvim.lua")); got != "vim.opt.number = true\n" {
		t.Errorf("a cancelled run changed .nvim.lua: %q", got)
	}
}

// Boundary: an expired observation fails a developer-owned file as cancellation does, wrapped
// or not, while any other observation error still leaves it preserved and unwritten.
func TestPrepareEditorWrite_Boundary_StoppedObservationIsNotPreserved(t *testing.T) {
	file := GeneratedFile{Path: ".nvim.lua", Editor: EditorNeovim, Content: "-- template\n"}
	for _, stop := range []error{context.DeadlineExceeded, fmt.Errorf("open .nvim.lua: %w", context.Canceled)} {
		if write, err := prepareEditorWrite(file, ".nvim.lua", nil, false, stop); !errors.Is(err, stop) {
			t.Errorf("observation %v = %+v, %v; want the error", stop, write.result, err)
		}
	}
	write, err := prepareEditorWrite(file, ".nvim.lua", nil, false, fs.ErrPermission)
	if err != nil || write.write || write.result.Outcome != WritePreserved {
		t.Fatalf("unreadable developer-owned file = %+v, %v; want PRESERVED and unwritten", write, err)
	}
	other := GeneratedFile{Path: ".vscode/tasks.json", Editor: EditorVSCode, Content: "{}\n"}
	if _, err := prepareEditorWrite(other, ".vscode/tasks.json", nil, false, fs.ErrPermission); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("unreadable managed file = %v, want the error", err)
	}
}
