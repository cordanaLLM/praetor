package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/editor"
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
	if err := editor.Write(set, root); err != nil {
		return fmt.Errorf("failed writing editor configurations: %w", err)
	}

	// editor.Write deliberately preserves a pre-existing .editorconfig/.clang-tidy and
	// truncates at its own file cap, so the synthesized file list is not the written
	// file list. Each file is classified from what is actually on disk.
	written, preserved := reportEditorFiles(set, root)
	fmt.Printf("[OK] Generated %d of %d IDE configuration file(s) across: %v\n",
		written, len(set.Files), set.Editors)
	if preserved > 0 {
		fmt.Printf("     %d file(s) left untouched because they already exist or exceed the write cap.\n", preserved)
	}
	return nil
}

func runEditorsVerify(opts editor.Options, root string) error {
	set, err := editor.Synthesize(opts)
	if err != nil {
		return fmt.Errorf("failed synthesizing editor configurations: %w", err)
	}
	if err := editor.Verify(set, root); err != nil {
		return fmt.Errorf("[FAIL] Editor configurations out of sync: %w", err)
	}
	fmt.Println("[PASS] All declared editor configurations verified in sync.")
	return nil
}

// reportEditorFiles prints and counts the per-file outcome of a generate run by comparing
// the synthesized content against what the workspace now holds.
func reportEditorFiles(set *editor.EditorConfigSet, root string) (written, preserved int) {
	if set == nil {
		return 0, 0
	}
	for i := 0; i < len(set.Files) && i < maxReportedEditorFiles; i++ {
		f := set.Files[i]
		full := filepath.Join(root, f.Path)
		status := "SKIPPED"
		switch onDisk, err := os.ReadFile(full); { // #nosec G304 -- path is the caller's workspace root joined with a synthesized relative config path.
		case err == nil && string(onDisk) == f.Content:
			status = "WRITTEN"
			written++
		case err == nil:
			status = "PRESERVED"
			preserved++
		default:
			preserved++
		}
		fmt.Printf("  - [%s] [%s] %s\n", status, f.Editor, f.Path)
	}
	return written, preserved
}
