package main

import (
	"flag"
	"fmt"

	"github.com/cordanallm/praetor/internal/editor"
)

func runEditors(args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: standardsctl editors <generate|verify> [arguments]")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	fs := flag.NewFlagSet("editors "+sub, flag.ContinueOnError)
	path := fs.String("path", ".", "Workspace root directory")
	if err := fs.Parse(subArgs); err != nil {
		return err
	}

	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = *path

	switch sub {
	case "generate":
		set, err := editor.Synthesize(opts)
		if err != nil {
			return fmt.Errorf("failed synthesizing editor configurations: %w", err)
		}
		if err := editor.Write(set, *path); err != nil {
			return fmt.Errorf("failed writing editor configurations: %w", err)
		}
		fmt.Printf("[OK] Successfully generated %d IDE configurations across: %v\n", len(set.Files), set.Editors)
		for _, f := range set.Files {
			fmt.Printf("  - [%s] %s\n", f.Editor, f.Path)
		}
		return nil

	case "verify":
		set, err := editor.Synthesize(opts)
		if err != nil {
			return fmt.Errorf("failed synthesizing editor configurations: %w", err)
		}
		if err := editor.Verify(set, *path); err != nil {
			return fmt.Errorf("[FAIL] Editor configurations out of sync: %w", err)
		}
		fmt.Println("[PASS] All declared editor configurations verified in sync.")
		return nil

	default:
		return fmt.Errorf("unknown editors command: %s", sub)
	}
}
