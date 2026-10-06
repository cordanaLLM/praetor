package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// receiptRecord is the status record the gate's untracked receipt produces.
const receiptRecord = "?? " + util.GateReceiptFile + "\x00"

// Positive: an owner checkout the gate ran in carries the untracked receipt, which .gitignore
// deliberately leaves visible (#136). init writes the overlay beside it and leaves it alone, and
// plan and prepare accept the committed overlay with the receipt still present.
func TestRunInit_Positive_ToleratesTheGateReceipt(t *testing.T) {
	f, opts := newInitFixture(t)
	testWrite(t, opts.OwnerPath, util.GateReceiptFile, "{\"receipt\":true}\n")
	r, err := Run(context.Background(), "init", opts)
	if err != nil || r.Status != "initialized" {
		t.Fatalf("init refused a checkout holding only the gate receipt: %+v %v", r, err)
	}
	if untracked := testGit(t, f.git, opts.OwnerPath, "ls-files", "--others"); untracked != util.GateReceiptFile {
		t.Fatalf("init touched the untracked set: %q", untracked)
	}
	testGit(t, f.git, opts.OwnerPath, "add", "--update")
	testGit(t, f.git, opts.OwnerPath, "-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "commit", "-m", "overlay")
	ownerSHA := testGit(t, f.git, opts.OwnerPath, "rev-parse", "HEAD")
	plan, err := Run(context.Background(), "plan", Options{OwnerPath: opts.OwnerPath, SourcePath: f.opts.SourcePath,
		OwnerSHA: ownerSHA, BaseSHA: f.opts.SourceSHA, SourceSHA: f.opts.SourceSHA})
	if err != nil || plan.Status != "planned" {
		t.Fatalf("plan refused an owner checkout holding the gate receipt: %+v %v", plan, err)
	}
}

func TestRunPrepare_Positive_ToleratesTheGateReceipt(t *testing.T) {
	f := newSyncFixture(t)
	testWrite(t, f.opts.OwnerPath, util.GateReceiptFile, "{\"receipt\":true}\n")
	if r, err := Run(context.Background(), "prepare", f.opts); err != nil {
		t.Fatalf("prepare refused an owner checkout holding the gate receipt: %+v %v", r, err)
	}
}

// Negative: the receipt excuses nothing else. Another untracked file beside it, the receipt's
// name below the root, a staged receipt and a symbolic link at the receipt path are all refused,
// and the refused init writes nothing.
func TestRunInit_Negative_ReceiptToleranceIsExact(t *testing.T) {
	cases := map[string]func(*testing.T, *syncFixture, string){
		"other untracked file": func(t *testing.T, _ *syncFixture, owner string) {
			testWrite(t, owner, util.GateReceiptFile, "{}\n")
			testWrite(t, owner, "note.txt", "x")
		},
		"receipt in a subdirectory": func(t *testing.T, _ *syncFixture, owner string) {
			testWrite(t, owner, filepath.Join("sub", util.GateReceiptFile), "{}\n")
		},
		"staged receipt": func(t *testing.T, f *syncFixture, owner string) {
			testWrite(t, owner, util.GateReceiptFile, "{}\n")
			testGit(t, f.git, owner, "add", "--", util.GateReceiptFile)
		},
		"symlink at the receipt path": func(t *testing.T, _ *syncFixture, owner string) {
			// The link targets a tracked, unmodified file, so the link is the only change.
			if err := os.Symlink("engine.txt", filepath.Join(owner, util.GateReceiptFile)); err != nil {
				// Windows without Developer Mode or the symlink privilege cannot create one;
				// the regular-file half of the contract is still replayed by the other cases.
				t.Skipf("symlinks unavailable on this host: %v", err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f, opts := newInitFixture(t)
			change(t, &f, opts.OwnerPath)
			before := testGit(t, f.git, opts.OwnerPath, "status", "--porcelain")
			r, err := Run(context.Background(), "init", opts)
			if err == nil || r != nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
				t.Fatalf("init on %s: %+v %v", name, r, err)
			}
			if after := testGit(t, f.git, opts.OwnerPath, "status", "--porcelain"); after != before {
				t.Fatalf("refused init wrote to the checkout: %q -> %q", before, after)
			}
		})
	}
}

// Boundary: the post-write check drops exactly one record, and only while the root holds the
// receipt as a regular file. A directory or a missing file at that path keeps the record, and
// the record must match whole -- a longer name or another status code is a change.
func TestWithoutGateReceipt_Boundary(t *testing.T) {
	exact := " M .devcontainer/devcontainer.json\x00 M .paperclip/harness.json\x00 M .paperclip/rules.md\x00 M .standards.yaml\x00"
	regular := t.TempDir()
	testWrite(t, regular, util.GateReceiptFile, "{}\n")
	if err := checkInitStatus(withoutGateReceipt(regular, exact+receiptRecord)); err != nil {
		t.Fatalf("the overlay beside a regular receipt was refused: %v", err)
	}
	if got := withoutGateReceipt(regular, receiptRecord); got != "" {
		t.Fatalf("a lone receipt record survived: %q", got)
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, util.GateReceiptFile), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ root, status string }{
		"missing file":  {t.TempDir(), receiptRecord},
		"directory":     {directory, receiptRecord},
		"longer name":   {regular, "?? " + util.GateReceiptFile + ".bak\x00"},
		"staged":        {regular, "A  " + util.GateReceiptFile + "\x00"},
		"second record": {regular, receiptRecord + receiptRecord},
	} {
		if got := withoutGateReceipt(tc.root, tc.status); got == "" {
			t.Errorf("%s: the record was dropped", name)
		}
	}
}
