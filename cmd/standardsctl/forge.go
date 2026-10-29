package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxAnalyzedCommits bounds the commit range a single HISS-14 check walks (HISS-02).
const maxAnalyzedCommits = 1000

// NUL frames records; a unit separator distinguishes the SHA from the message.
// Malformed framing fails closed instead of silently dropping a commit.
const (
	commitRecordSep = "\x00"
	commitFieldSep  = "\x1f"
)

func printForgeUsage() {
	fmt.Println("Usage: standardsctl forge <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  sync-wiki [--output=docs/wiki]        Generate git-backed wiki documentation suite")
	fmt.Println("  validate-pr <pr-body-file>            Verify HISS checklist and the Ed25519 Exit-0 receipt")
	fmt.Println("  check-commits --base=<ref> [--head=HEAD]  Enforce the commit message policy on a commit range")
	fmt.Println("  refactor-check --base=<ref> [--head=HEAD] [--run-tests]  Check refactor commits in a range")
	fmt.Println("  check-message <commit-message-file>   Enforce the commit message policy on one message (commit-msg hook)")
	fmt.Println("  claims migrate --repo OWNER/NAME --issue N [--issue N ...] [--apply]")
	fmt.Println("                                        Migrate legacy plain claim comments to hashed tags")
}

func runForge(args []string) error {
	if len(args) < 1 {
		printForgeUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	timeout := 2 * time.Minute
	if sub == "refactor-check" {
		timeout = 35 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printForgeUsage()
		return nil
	case "sync-wiki":
		return runForgeSyncWiki(ctx, subArgs)
	case "validate-pr":
		return runForgeValidatePR(subArgs)
	case "check-commits":
		return runForgeCheckCommits(ctx, subArgs)
	case "refactor-check":
		return runForgeRefactorCheck(ctx, subArgs)
	case "check-message":
		return runForgeCheckMessage(subArgs)
	case "claims", "claim":
		return runForgeClaims(ctx, subArgs)
	default:
		return fmt.Errorf("unknown forge subcommand: %s", sub)
	}
}

func runForgeSyncWiki(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forge sync-wiki", flag.ContinueOnError)
	outputDir := fs.String("output", "docs/wiki", "Output directory for generated wiki; a relative path must stay inside the repository")
	settings := registerOperatorSettingsFlags(fs)
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("forge sync-wiki accepts no positional arguments, got %q", rest)
	}
	forgeSettings, err := loadForgeSettings(ctx, settings)
	if err != nil {
		return fmt.Errorf("forge sync-wiki: %w", err)
	}

	manifest, err := forge.GenerateWiki(ctx, ".", *outputDir, forgeSettings.DefaultOwner)
	if err != nil {
		return fmt.Errorf("failed generating wiki: %w", err)
	}

	fmt.Printf("[OK] Generated %d wiki pages in %s:\n", len(manifest.Pages), manifest.OutputDir)
	for _, p := range manifest.Pages {
		fmt.Printf("  - %s: %s\n", p.Name, p.Title)
	}
	return nil
}

// resolvePinnedReceiptKey loads the pinned Ed25519 receipt public key from the manifest.
// A missing pin is only tolerated when the operator explicitly opted out, because without
// it the receipt is merely self-consistent and proves nothing about this repository.
func resolvePinnedReceiptKey(ctx context.Context, manifestPath string, allowUnpinned bool) (ed25519.PublicKey, error) {
	pinned, err := lockdown.PinnedPublicKey(ctx, manifestPath)
	if err == nil {
		return pinned, nil
	}
	if allowUnpinned {
		fmt.Println("[WARN] Receipt verified without a pinned key (--allow-unpinned): " +
			"the signature is self-consistent but not bound to this repository.")
		return nil, nil
	}
	return nil, fmt.Errorf("cannot verify the Exit-0 receipt: %w", err)
}

func closingPolicyForCI(ctx context.Context, repoDir, explicitToken, endpoint, explicitAuthor string) (forge.ClosingReferencePolicy, error) {
	token := resolveSyncToken(explicitToken)
	if token == "" || (explicitToken == "" && os.Getenv("GITHUB_ACTIONS") != "true") {
		return forge.ClosingReferencePolicy{}, nil
	}
	owner, name, err := config.ResolveRepositoryIdentity(ctx, repoDir, "", "")
	if err != nil {
		return forge.ClosingReferencePolicy{}, fmt.Errorf("closing reference resolution: %w", err)
	}
	driver := forge.NewGitHubDriver(token, endpoint)
	driver.SetRepository(owner, name)
	return forge.ClosingReferencePolicy{Resolver: driver, PullRequestAuthor: pullRequestAuthor(explicitAuthor)}, nil
}

func pullRequestAuthor(explicit string) string {
	if explicit != "" {
		return explicit
	}
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return ""
	}
	data, err := util.ReadFileLimited(filepath.Clean(path), maxCommitMessageBytes)
	if err != nil {
		return ""
	}
	var event struct {
		PullRequest struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return ""
	}
	return event.PullRequest.User.Login
}

func runForgeValidatePR(args []string) error {
	fs := flag.NewFlagSet("forge validate-pr", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml carrying receipt.public_key")
	headSHA := fs.String("head-sha", "", "Commit SHA the receipt must certify (the PR head)")
	allowUnpinned := fs.Bool("allow-unpinned", false, "Accept a receipt without checking it against the pinned public key")
	repoDir := fs.String("repo", ".", "Repository directory")
	token := fs.String("token", "", "Forge API token for closing-reference resolution in CI (default: GITHUB_TOKEN, then GH_TOKEN)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint for closing-reference resolution")
	prAuthor := fs.String("pr-author", "", "Author login of the pull request being checked (default: GITHUB_EVENT_PATH pull_request.user.login)")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: standardsctl forge validate-pr [--config=.standards.yaml] " +
			"[--head-sha=<sha>] <pr-body-file> (exactly one body file required)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := contextopt.ReadSnapshot(ctx, rest[0])
	if err != nil {
		return fmt.Errorf("failed to read PR body file: %w", err)
	}

	pinned, err := resolvePinnedReceiptKey(ctx, *configPath, *allowUnpinned)
	if err != nil {
		return err
	}

	closingPolicy, err := forgePolicyForCI(ctx, *repoDir, *configPath, *token, *endpoint, *prAuthor)
	if err != nil {
		return err
	}
	// The closing references are resolved at the forge, one request each: they get a deadline of
	// their own instead of the ten seconds the body read and the key lookup share.
	validateCtx, cancelValidate := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelValidate()
	res, err := forge.ValidatePRChecklistWithPolicyContext(validateCtx, string(data),
		forge.ReceiptPolicy{PinnedKey: pinned, HeadSHA: *headSHA, ClosingReferences: closingPolicy})
	if err != nil && res == nil {
		return fmt.Errorf("PR checklist validation failed: %w", err)
	}
	return reportPRChecklist(res)
}

// reportPRChecklist prints the checklist result with its warnings and returns the failure of an
// invalid one.
func reportPRChecklist(res *forge.PRChecklistResult) error {
	fmt.Println("=== PR Checklist Validation ===")
	fmt.Printf("  HISS-16 Invariant Check: %v\n", res.HasHISS16Check)
	fmt.Printf("  3D Tests (Pos/Neg/Bound): %v\n", res.Has3DTestsCheck)
	fmt.Printf("  Ed25519 Exit-0 Receipt:  %v\n", res.HasReceipt)
	if res.ReceiptProof != "" {
		fmt.Printf("  Verified Receipt:        %s\n", res.ReceiptProof)
	}
	for i := 0; i < len(res.Warnings); i++ {
		fmt.Printf("[WARN] %s\n", res.Warnings[i])
	}
	if !res.Valid {
		return fmt.Errorf("PR validation failed: %s", strings.Join(res.Errors, "; "))
	}
	fmt.Println("[PASS] PR checklist fulfills all mandatory governance invariants.")
	return nil
}

// commitRange reads the commit messages of base..head through the audited git wrapper.
func commitRange(ctx context.Context, repoDir, base, head string) ([]string, error) {
	for _, ref := range []string{base, head} {
		if err := util.ValidateExecArg(ref); err != nil {
			return nil, fmt.Errorf("invalid git revision %q: %w", ref, err)
		}
	}
	format := "--format=%H%x1f%B%x00"
	limit := fmt.Sprintf("--max-count=%d", maxAnalyzedCommits+1)
	out, err := util.RunGit(ctx, repoDir, "log", limit, format, base+".."+head, "--")
	if err != nil {
		return nil, fmt.Errorf("failed reading commit range %s..%s: %w", base, head, err)
	}
	records := strings.Split(out, commitRecordSep)
	messages := make([]string, 0, len(records))
	for i := 0; i < len(records) && i <= maxAnalyzedCommits; i++ {
		record := strings.TrimSpace(records[i])
		if record == "" {
			continue
		}
		if _, _, ok := strings.Cut(record, commitFieldSep); !ok {
			return nil, errors.New("malformed commit record in git log output")
		}
		messages = append(messages, record)
	}
	if len(messages) > maxAnalyzedCommits || len(records) > maxAnalyzedCommits+1 {
		return nil, fmt.Errorf("commit range exceeds maximum of %d commits; split the range", maxAnalyzedCommits)
	}
	return messages, nil
}

// runForgeCheckCommits applies the commit message policy (forge.AnalyzeCommit) to every commit in
// the range: a Conventional Commits subject, and the HISS-14 Migration: footer on a breaking
// change. CI runs it over every commit of a pull request, so a commit made with its commit-msg
// hook skipped still meets the policy the hook enforces (#61).
func runForgeCheckCommits(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forge check-commits", flag.ContinueOnError)
	base := fs.String("base", "", "Base revision of the range to analyze (required)")
	head := fs.String("head", "HEAD", "Head revision of the range to analyze")
	repoDir := fs.String("repo", ".", "Repository directory")
	token := fs.String("token", "", "Forge API token for closing-reference resolution in CI (default: GITHUB_TOKEN, then GH_TOKEN)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint for closing-reference resolution")
	prAuthor := fs.String("pr-author", "", "Author login of the pull request being checked (default: GITHUB_EVENT_PATH pull_request.user.login)")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("forge check-commits accepts no positional arguments, got %q", rest)
	}
	if *base == "" {
		return errors.New("usage: standardsctl forge check-commits --base=<ref> [--head=HEAD]")
	}

	messages, err := commitRange(ctx, *repoDir, *base, *head)
	if err != nil {
		return err
	}
	closingPolicy, err := forgePolicyForCI(ctx, *repoDir, filepath.Join(*repoDir, config.ManifestFileName), *token, *endpoint, *prAuthor)
	if err != nil {
		return err
	}

	var failures []string
	for i := 0; i < len(messages); i++ {
		failure, err := commitMessageFailure(ctx, messages[i], closingPolicy)
		if err != nil {
			return err
		}
		if failure != "" {
			failures = append(failures, failure)
		}
	}

	fmt.Printf("=== Commit Message Policy: %d commit(s) in %s..%s ===\n", len(messages), *base, *head)
	if len(failures) > 0 {
		return fmt.Errorf("commit message policy violation: %d of %d commit(s) fail, first %s", len(failures), len(messages), failures[0])
	}
	fmt.Println("[PASS] Every commit carries a Conventional Commits subject, and every breaking commit a mandatory Migration: footer.")
	return nil
}

// commitMessageFailure applies the commit message policy to one entry of commitRange, prints its
// warnings and its failure line, and returns that line, empty for a commit that passes.
func commitMessageFailure(ctx context.Context, entry string, closingPolicy forge.ClosingReferencePolicy) (string, error) {
	sha, message, _ := strings.Cut(entry, commitFieldSep)
	analysis, err := forge.AnalyzeCommitWithPolicy(ctx, message, closingPolicy)
	if err != nil {
		return "", fmt.Errorf("failed analyzing commit %s: %w", sha, err)
	}
	for i := 0; i < len(analysis.Warnings); i++ {
		fmt.Printf("[WARN] %s: %s\n", buildid.Short(sha), analysis.Warnings[i])
	}
	if analysis.Valid {
		return "", nil
	}
	failure := buildid.Short(sha) + ": " + strings.Join(analysis.Errors, "; ")
	for i := 0; i < len(analysis.Errors) && i < maxCommitMessageBytes; i++ {
		fmt.Printf("[FAIL] %s: %s\n", buildid.Short(sha), analysis.Errors[i])
	}
	return failure, nil
}

// maxCommitMessageBytes bounds the commit message file check-message reads.
const maxCommitMessageBytes = 1 << 20

// runForgeCheckMessage applies the commit message policy to the message file git hands the
// commit-msg hook, after git's default cleanup (forge.CleanCommitMessage). It is the hook's half
// of the policy check-commits applies in CI.
func runForgeCheckMessage(args []string) error {
	fs := flag.NewFlagSet("forge check-message", flag.ContinueOnError)
	repoDir := fs.String("repo", ".", "Repository directory")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: standardsctl forge check-message <commit-message-file> (exactly one file required)")
	}
	data, err := util.ReadFileLimited(rest[0], maxCommitMessageBytes)
	if err != nil {
		return fmt.Errorf("read commit message %s: %w", rest[0], err)
	}
	message, err := forge.CleanCommitMessage(string(data))
	if err != nil {
		return fmt.Errorf("commit message %s: %w", rest[0], err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	policy, err := forgeTextPolicy(ctx, *repoDir, filepath.Join(*repoDir, config.ManifestFileName), forge.ClosingReferencePolicy{})
	if err != nil {
		return err
	}
	analysis, err := forge.AnalyzeCommitWithPolicy(ctx, message, policy)
	if err != nil {
		return fmt.Errorf("commit message %s: %w", rest[0], err)
	}
	for _, warning := range analysis.Warnings {
		fmt.Printf("[WARN] %s\n", warning)
	}
	if !analysis.Valid {
		for _, reason := range analysis.Errors {
			fmt.Printf("[FAIL] %s\n", reason)
		}
		return fmt.Errorf("commit message policy violation: %s", strings.Join(analysis.Errors, "; "))
	}
	fmt.Println("[PASS] The commit message meets the commit message policy.")
	return nil
}
