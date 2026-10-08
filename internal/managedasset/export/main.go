// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command export writes every managed asset (internal/managedasset.Assets) under a directory at
// its repository-relative path, and the draft skip rendering of each hosted workflow
// (managedasset.DraftSkipAssets) below draft-skip/, and prints the paths, one per line. The lint harness
// scripts/test_emitted_hook_lint.py runs it to lint the files adoption writes, so the set it
// lints is the registry and no list of it exists elsewhere (HISS-19).
//
//	go run ./internal/managedasset/export <empty directory>
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// draftSkipDir is the directory below the export root that holds the draft skip rendering of
// each hosted workflow (managedasset.DraftSkipAssets), so it never collides with the
// fail-closed rendering at the real path.
const draftSkipDir = "draft-skip"

// assetMode and dirMode are the permissions of an exported file and its directories.
const (
	assetMode = 0o600
	dirMode   = 0o750
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run exports into the one directory named by args and returns the exit status.
func run(args []string, stdout, stderr io.Writer) int {
	report := log.New(stderr, "export: ", 0)
	if len(args) != 1 || args[0] == "" {
		report.Print("usage: export <directory>")
		return 2
	}
	paths, err := export(args[0])
	if err != nil {
		report.Print(err)
		return 1
	}
	for _, rel := range paths {
		if _, err := fmt.Fprintln(stdout, rel); err != nil {
			report.Printf("list %s: %v", rel, err)
			return 1
		}
	}
	return 0
}

// export writes every managed asset under dir and returns their paths. The tree is written
// through a handle on dir, so no path leaves it, and a file that exists already is an error,
// so a stale tree is never mixed in.
func export(dir string) (paths []string, err error) {
	assets, err := managedasset.Assets()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	skips, err := managedasset.DraftSkipAssets()
	if err != nil {
		return nil, err
	}
	for _, skip := range skips {
		skip.Path = path.Join(draftSkipDir, skip.Path)
		assets = append(assets, skip)
	}
	paths = make([]string, 0, len(assets))
	for _, asset := range assets {
		if err := writeAsset(root, asset); err != nil {
			return nil, err
		}
		paths = append(paths, asset.Path)
	}
	return paths, nil
}

// writeAsset creates one asset below root, with its directories.
func writeAsset(root *os.Root, asset managedasset.Asset) error {
	if err := root.MkdirAll(path.Dir(asset.Path), dirMode); err != nil {
		return fmt.Errorf("create the directory of %s: %w", asset.Path, err)
	}
	file, err := root.OpenFile(asset.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, assetMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", asset.Path, err)
	}
	_, err = file.Write(asset.Data)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", asset.Path, err)
	}
	return nil
}
