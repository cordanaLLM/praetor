// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/releasesmoke"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runReleaseSmoke(args []string) error {
	fs := flag.NewFlagSet("release smoke", flag.ContinueOnError)
	repo := fs.String("repo", "", "Release repository (OWNER/NAME)")
	tag := fs.String("tag", "", "Published release tag")
	allow := fs.Bool("allow-no-assets", false, "Explicitly allow an asset-less release")
	maxBytes := fs.Int64("max-bytes", releasesmoke.DefaultMaxBytes, "Total asset download byte limit (default 4 GiB)")
	positional, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return fmt.Errorf("release smoke: unexpected arguments %q", positional)
	}
	owner, name, ok := strings.Cut(*repo, "/")
	if !ok {
		return errors.New("release smoke: --repo=OWNER/NAME is required")
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, name); err != nil {
		return fmt.Errorf("release smoke: %w", err)
	}
	if *tag == "" {
		return errors.New("release smoke: --tag=TAG is required")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	driver := forge.NewGitHubDriver(token, "")
	driver.SetRepository(owner, name)
	driver.HTTPClient.Timeout = 2 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	result, err := releasesmoke.Run(ctx, driver, releasesmoke.Options{Tag: *tag, AllowNoAssets: *allow, MaxBytes: *maxBytes})
	for i := 0; i < len(result.Lines); i++ {
		fmt.Println(result.Lines[i])
	}
	return err
}
