// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

const figuresUsage = `Usage: praetorctl figures pack --out=FILE

Packs the embedded figure player bundle (loader.js, player.js, THIRD-PARTY-LICENSES.txt)
into a deterministic figures-player.tar.gz release archive and prints its sha256sum line.`

func runFigures(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Println(figuresUsage)
		if len(args) == 0 {
			return errors.New("figures requires a subcommand: pack")
		}
		return nil
	}
	if args[0] != "pack" {
		return fmt.Errorf("unknown figures subcommand %q (expected pack)", args[0])
	}
	return runFiguresPack(args[1:])
}

func runFiguresPack(args []string) error {
	fs := flag.NewFlagSet("figures pack", flag.ContinueOnError)
	out := fs.String("out", "", "Output path for "+figureassets.PlayerArchiveName)
	var usageErr error
	fs.Usage = func() {
		_, usageErr = fmt.Fprintf(fs.Output(), "%s\n\n", figuresUsage)
		fs.PrintDefaults()
	}
	if len(args) > 0 && isHelpToken(args[0]) {
		fs.SetOutput(os.Stdout)
		fs.Usage()
		return usageErr
	}
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return errors.Join(err, usageErr)
	}
	if len(positional) > 0 {
		return fmt.Errorf("figures pack accepts no positional arguments, got %q", positional)
	}
	target := strings.TrimSpace(*out)
	if target == "" {
		return errors.New("figures pack requires --out <file>")
	}
	archive, err := figureassets.PlayerArchive()
	if err != nil {
		return fmt.Errorf("build player archive: %w", err)
	}
	digest, err := figureassets.PlayerArchiveSHA256()
	if err != nil {
		return fmt.Errorf("digest player archive: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fmt.Errorf("create output directory for %s: %w", target, err)
	}
	if err := util.WriteFileAt(target, archive, 0); err != nil {
		return fmt.Errorf("write player archive %s: %w", target, err)
	}
	fmt.Printf("%s  %s\n", digest, figureassets.PlayerArchiveName)
	return nil
}
