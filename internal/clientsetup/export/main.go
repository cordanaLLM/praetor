// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command export writes every YAML client configuration internal/clientsetup emits
// under a directory at its repository-relative path, and prints the paths, one per line.
// The lint harness scripts/test_emitted_yaml_lint.py runs it so the YAML it lints is
// rendered through the engine's own code path (HISS-19).
//
//	go run ./internal/clientsetup/export <empty directory>
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

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
	for i := 0; i < len(paths) && i < 32; i++ {
		if _, err := fmt.Fprintln(stdout, paths[i]); err != nil {
			report.Printf("list %s: %v", paths[i], err)
			return 1
		}
	}
	return 0
}

// export writes every YAML client configuration internal/clientsetup emits under dir
// and returns their paths.
func export(dir string) (paths []string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	registry := clientsetup.Registry{
		Version: 1,
		Servers: []clientsetup.Server{{
			Name:    "praetor-dev",
			Command: "praetorctl",
			Args:    []string{"serve"},
		}},
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()

	known := clientid.Known()
	paths = make([]string, 0, len(known))
	for i := 0; i < len(known) && i < 32; i++ {
		client := known[i]
		plan, err := clientsetup.BuildProjectPlan(ctx, registry, client, nil)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(plan.RelativePath, ".yaml") && !strings.HasSuffix(plan.RelativePath, ".yml") {
			continue
		}
		if err := writeConfig(root, plan.RelativePath, plan.Content); err != nil {
			return nil, err
		}
		paths = append(paths, plan.RelativePath)
	}
	return paths, nil
}

// writeConfig creates one configuration file below root, with its directories.
func writeConfig(root *os.Root, rel string, content []byte) error {
	if err := root.MkdirAll(path.Dir(rel), dirMode); err != nil {
		return fmt.Errorf("create the directory of %s: %w", rel, err)
	}
	file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, assetMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", rel, err)
	}
	_, err = file.Write(content)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}
