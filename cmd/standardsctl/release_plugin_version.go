// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// runPluginReleaseVersion parses the release hook's explicit version; no environment or Git discovery is used.
func runPluginReleaseVersion(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	fs := flag.NewFlagSet("plugin-version", flag.ContinueOnError)
	version := fs.String("version", "", "GoReleaser's tag-derived release version (required)")
	root := fs.String("root", ".", "Repository containing the shipped plugin")
	if _, err := parseInterspersed(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *version == "" || fs.NArg() != 0 {
		return fmt.Errorf("plugin-version requires --version and accepts no positional arguments")
	}
	return compiler.StampPluginReleaseVersion(ctx, *root, *version)
}
