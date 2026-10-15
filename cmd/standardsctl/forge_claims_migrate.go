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

const maxOwnSessionsLimit = 64

type ownSessionsFlag []string

func (f *ownSessionsFlag) String() string {
	parts := make([]string, 0, len(*f))
	for i := 0; i < len(*f) && i < maxOwnSessionsLimit; i++ {
		parts = append(parts, (*f)[i])
	}
	return strings.Join(parts, ",")
}

func (f *ownSessionsFlag) Set(val string) error {
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(*f) >= maxOwnSessionsLimit {
			return fmt.Errorf("too many --own-session names: maximum is %d", maxOwnSessionsLimit)
		}
		if err := forge.ValidateClaimSession(part); err != nil {
			return fmt.Errorf("invalid --own-session %q: %w", part, err)
		}
		*f = append(*f, part)
	}
	return nil
}

func printForgeClaimsUsage() {
	fmt.Println("Usage: standardsctl forge claims <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  migrate --repo OWNER/NAME --issue N [--issue N ...] [--own-session NAME ...] [--apply]")
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
	repo        string
	issues      issueNumbersFlag
	ownSessions ownSessionsFlag
	apply       bool
	token       string
	endpoint    string
}

func parseMigrateClaimsFlags(args []string) (migrateClaimsOptions, error) {
	fs := flag.NewFlagSet("forge claims migrate", flag.ContinueOnError)
	repo := fs.String("repo", "", "Repository in OWNER/NAME format (required)")
	var issues issueNumbersFlag
	fs.Var(&issues, "issue", "Issue number to migrate (can be specified multiple times)")
	var ownSessions ownSessionsFlag
	fs.Var(&ownSessions, "own-session", "Session name owned by this host (repeatable, up to 64)")
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
		repo:        *repo,
		issues:      issues,
		ownSessions: ownSessions,
		apply:       *apply,
		token:       *tokenFlag,
		endpoint:    *endpoint,
	}, nil
}

func checkMigrateAuth(apply bool, token, tokenFlag string) error {
	if apply && token == "" && tokenFlag == "" {
		return errors.New("forge claims migrate --apply needs a forge token: set GITHUB_TOKEN, sign in with gh, or pass --token")
	}
	return nil
}

func migrateAllIssues(ctx context.Context, f forge.ClaimForge, owner, repo string, issues []int, apply bool, key []byte, ownSessions []string) error {
	var totalMigrated, totalSkipped int
	for i := 0; i < len(issues) && i < forge.MaxIssuesLimit; i++ {
		issueNum := issues[i]
		counts, err := forge.MigrateIssueClaims(ctx, f, issueNum, apply, key, ownSessions, claimOutput)
		if err != nil {
			return fmt.Errorf("migrate issue %s/%s#%d: %w", owner, repo, issueNum, err)
		}
		totalMigrated += counts.Migrated
		totalSkipped += counts.SkippedOtherHost
	}
	if claimOutput != nil {
		if _, err := fmt.Fprintf(claimOutput, "migrated=%d skipped-other-host=%d\n", totalMigrated, totalSkipped); err != nil {
			return err
		}
	}
	return nil
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
	if err := checkMigrateAuth(opts.apply, token, opts.token); err != nil {
		return err
	}
	f, err := claimsMigrateOpener(token, opts.endpoint)(owner, repoName)
	if err != nil {
		return fmt.Errorf("open forge for %s/%s: %w", owner, repoName, err)
	}
	key, err := forge.LoadOrCreateDefaultClaimKey()
	if err != nil {
		return fmt.Errorf("load claim key: %w", err)
	}
	return migrateAllIssues(ctx, f, owner, repoName, opts.issues, opts.apply, key, opts.ownSessions)
}
