package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxReportedEditorFiles is the scalar upper bound (HISS-02) on the per-file lines the
// editors report prints.
const maxReportedEditorFiles = 512

func runEditors(args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: praetorctl editors <generate|verify> [--path=.]")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	fs := flag.NewFlagSet("editors "+sub, flag.ContinueOnError)
	path := fs.String("path", ".", "Workspace root directory")
	positional, err := parseInterspersed(fs, subArgs)
	if err != nil {
		return err
	}
	root := positionalAt(positional, 0, *path)

	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = root

	switch sub {
	case "generate":
		return runEditorsGenerate(opts, root)
	case "verify":
		return runEditorsVerify(opts, root)
	default:
		return fmt.Errorf("unknown editors command: %s", sub)
	}
}

func runEditorsGenerate(opts editor.Options, root string) error {
	set, err := editor.Synthesize(opts)
	if err != nil {
		return fmt.Errorf("failed synthesizing editor configurations: %w", err)
	}
	before := snapshotEditorFiles(set, root)
	if err := editor.Write(set, root); err != nil {
		return fmt.Errorf("failed writing editor configurations: %w", err)
	}

	created, merged, present := reportEditorFiles(set, root, before)
	fmt.Printf("[OK] Editor requirements: %d created, %d merged, %d already present across: %v\n",
		created, merged, present, set.Editors)
	return nil
}

func runEditorsVerify(opts editor.Options, root string) error {
	set, err := editor.Synthesize(opts)
	if err != nil {
		return fmt.Errorf("failed synthesizing editor configurations: %w", err)
	}
	report, err := editor.VerifyWithReport(set, root)
	if err != nil {
		return fmt.Errorf("[FAIL] Editor configurations out of sync: %w", err)
	}
	fmt.Printf("[PASS] Managed requirements verified in %d editor configuration file(s).\n", len(report.Verified))
	if len(report.PreservedUnverified) > 0 {
		fmt.Printf("[UNVERIFIED] Preserved %d non-JSON file(s) without semantic verification: %v\n",
			len(report.PreservedUnverified), report.PreservedUnverified)
	}
	return nil
}

// editorFileSnapshot records the pre-write state needed to distinguish a merge
// from a requirement that was already present.
type editorFileSnapshot struct {
	data   []byte
	exists bool
}

func snapshotEditorFiles(set *editor.EditorConfigSet, root string) map[string]editorFileSnapshot {
	result := make(map[string]editorFileSnapshot)
	if set == nil {
		return result
	}
	for i := 0; i < len(set.Files) && i < maxReportedEditorFiles; i++ {
		file := set.Files[i]
		full, err := util.ConfinePath(root, file.Path)
		if err != nil {
			continue
		}
		data, err := contextopt.ReadSnapshot(context.Background(), full)
		if err == nil {
			result[file.Path] = editorFileSnapshot{data: data, exists: true}
		}
	}
	return result
}

func reportEditorFiles(set *editor.EditorConfigSet, root string, before map[string]editorFileSnapshot) (created, merged, present int) {
	if set == nil {
		return 0, 0, 0
	}
	for i := 0; i < len(set.Files) && i < maxReportedEditorFiles; i++ {
		f := set.Files[i]
		status := classifyEditorFile(root, f, before[f.Path])
		switch status {
		case "CREATED":
			created++
		case "MERGED":
			merged++
		case "PRESENT":
			present++
		}
		fmt.Printf("  - [%s] [%s] %s\n", status, f.Editor, f.Path)
	}
	return created, merged, present
}

// classifyEditorFile compares the pinned snapshots from before and after Write.
func classifyEditorFile(root string, f editor.GeneratedFile, before editorFileSnapshot) string {
	full, err := util.ConfinePath(root, f.Path)
	if err != nil {
		return "SKIPPED"
	}
	onDisk, err := contextopt.ReadSnapshot(context.Background(), full)
	switch {
	case err != nil:
		return "SKIPPED"
	case !before.exists:
		return "CREATED"
	case bytes.Equal(before.data, onDisk):
		return "PRESENT"
	default:
		return "MERGED"
	}
}
