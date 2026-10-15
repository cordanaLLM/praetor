// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// claimsMigrateOpener builds a ClaimForge opener; tests replace it with a fake forge.
var claimsMigrateOpener = defaultClaimsMigrateOpener

func defaultClaimsMigrateOpener(token, endpoint string) forge.ClaimForgeOpener {
	return func(owner, repo string) (forge.ClaimForge, error) {
		driver := forge.NewGitHubDriver(token, endpoint)
		driver.SetRepository(owner, repo)
		return driver, nil
	}
}

type issueNumbersFlag []int

func (f *issueNumbersFlag) String() string {
	parts := make([]string, 0, len(*f))
	for _, n := range *f {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

func (f *issueNumbersFlag) Set(val string) error {
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid issue number %q: must be positive integer", part)
		}
		*f = append(*f, n)
	}
	return nil
}

func printForgeClaimsUsage() {
	fmt.Println("Usage: standardsctl forge claims <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  migrate --repo OWNER/NAME --issue N [--issue N ...] [--apply]")
	fmt.Println("            Migrate legacy claim comments with plain identities to keyed HMAC-SHA256 tags")
}

func runForgeClaims(ctx context.Context, args []string) error {
	if len(args) < 1 {
		printForgeClaimsUsage()
		return nil
	}
	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "-h", "--help", "help":
		printForgeClaimsUsage()
		return nil
	case "migrate":
		return runForgeClaimsMigrate(ctx, subArgs)
	default:
		return fmt.Errorf("unknown forge claims subcommand: %s", sub)
	}
}

type migrateClaimsOptions struct {
	repo     string
	issues   issueNumbersFlag
	apply    bool
	token    string
	endpoint string
}

func parseMigrateClaimsFlags(args []string) (migrateClaimsOptions, error) {
	fs := flag.NewFlagSet("forge claims migrate", flag.ContinueOnError)
	repo := fs.String("repo", "", "Repository in OWNER/NAME format (required)")
	var issues issueNumbersFlag
	fs.Var(&issues, "issue", "Issue number to migrate (can be specified multiple times)")
	apply := fs.Bool("apply", false, "Apply the migration (default: dry run)")
	tokenFlag := fs.String("token", "", "Forge API token (default: GITHUB_TOKEN or gh auth token)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint (default: https://api.github.com)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return migrateClaimsOptions{}, err
	}
	if *repo == "" {
		return migrateClaimsOptions{}, errors.New("forge claims migrate requires --repo OWNER/NAME")
	}
	if len(issues) == 0 {
		return migrateClaimsOptions{}, errors.New("forge claims migrate requires at least one --issue")
	}
	return migrateClaimsOptions{
		repo:     *repo,
		issues:   issues,
		apply:    *apply,
		token:    *tokenFlag,
		endpoint: *endpoint,
	}, nil
}

func runForgeClaimsMigrate(ctx context.Context, args []string) error {
	opts, err := parseMigrateClaimsFlags(args)
	if err != nil {
		return err
	}
	owner, repoName, err := util.SplitGitHubRepository(opts.repo)
	if err != nil {
		return fmt.Errorf("invalid --repo %q: %w", opts.repo, err)
	}
	token := resolveForgeAuthToken(ctx, opts.token)
	if opts.apply && token == "" && opts.token == "" {
		return errors.New("forge claims migrate --apply needs a forge token: set GITHUB_TOKEN, sign in with gh, or pass --token")
	}
	f, err := claimsMigrateOpener(token, opts.endpoint)(owner, repoName)
	if err != nil {
		return fmt.Errorf("open forge for %s/%s: %w", owner, repoName, err)
	}
	key, err := forge.LoadOrCreateDefaultClaimKey()
	if err != nil {
		return fmt.Errorf("load claim key: %w", err)
	}
	for _, issueNum := range opts.issues {
		if err := forge.MigrateIssueClaims(ctx, f, issueNum, opts.apply, key, claimOutput); err != nil {
			return fmt.Errorf("migrate issue %s/%s#%d: %w", owner, repoName, issueNum, err)
		}
	}
	return nil
}
