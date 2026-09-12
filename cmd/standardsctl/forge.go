package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxAnalyzedCommits bounds the commit range a single HISS-14 check walks (HISS-02).
const maxAnalyzedCommits = 1000

// commitRecordSep and commitFieldSep are the ASCII record/unit separators used to frame
// `git log` output unambiguously, so a commit body can contain any text at all.
const (
	commitRecordSep = "\x1e"
	commitFieldSep  = "\x1f"
)

func printForgeUsage() {
	fmt.Println("Usage: standardsctl forge <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  sync-wiki [--output=docs/wiki]        Generate git-backed wiki documentation suite")
	fmt.Println("  validate-pr <pr-body-file>            Verify HISS checklist and the Ed25519 Exit-0 receipt")
	fmt.Println("  check-commits --base=<ref> [--head=HEAD]  Enforce the HISS-14 Migration: footer on a commit range")
}

func runForge(args []string) error {
	if len(args) < 1 {
		printForgeUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	default:
		return fmt.Errorf("unknown forge subcommand: %s", sub)
	}
}

func runForgeSyncWiki(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forge sync-wiki", flag.ContinueOnError)
	outputDir := fs.String("output", "docs/wiki", "Output directory for generated wiki")
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := forge.GenerateWiki(ctx, ".", *outputDir)
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
func resolvePinnedReceiptKey(manifestPath string, allowUnpinned bool) (ed25519.PublicKey, error) {
	pinned, err := lockdown.PinnedPublicKey(manifestPath)
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

func runForgeValidatePR(args []string) error {
	fs := flag.NewFlagSet("forge validate-pr", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml carrying receipt.public_key")
	headSHA := fs.String("head-sha", "", "Commit SHA the receipt must certify (the PR head)")
	allowUnpinned := fs.Bool("allow-unpinned", false, "Accept a receipt without checking it against the pinned public key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("usage: standardsctl forge validate-pr [--config=.standards.yaml] " +
			"[--head-sha=<sha>] <pr-body-file>")
	}

	// #nosec G304 -- the PR body file is an operator-supplied argument read as plain text.
	data, err := os.ReadFile(rest[0])
	if err != nil {
		return fmt.Errorf("failed to read PR body file: %w", err)
	}

	pinned, err := resolvePinnedReceiptKey(*configPath, *allowUnpinned)
	if err != nil {
		return err
	}

	res, err := forge.ValidatePRChecklistWithPolicy(string(data),
		forge.ReceiptPolicy{PinnedKey: pinned, HeadSHA: *headSHA})
	if err != nil && res == nil {
		return fmt.Errorf("PR checklist validation failed: %w", err)
	}
	fmt.Println("=== PR Checklist Validation ===")
	fmt.Printf("  HISS-16 Invariant Check: %v\n", res.HasHISS16Check)
	fmt.Printf("  3D Tests (Pos/Neg/Bound): %v\n", res.Has3DTestsCheck)
	fmt.Printf("  Ed25519 Exit-0 Receipt:  %v\n", res.HasReceipt)
	if res.ReceiptProof != "" {
		fmt.Printf("  Verified Receipt:        %s\n", res.ReceiptProof)
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
	format := "--format=%H" + commitFieldSep + "%B" + commitRecordSep
	out, err := util.RunGit(ctx, repoDir, "log", format, base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("failed reading commit range %s..%s: %w", base, head, err)
	}
	records := strings.Split(out, commitRecordSep)
	messages := make([]string, 0, len(records))
	for i := 0; i < len(records) && i < maxAnalyzedCommits; i++ {
		record := strings.TrimSpace(records[i])
		if record == "" {
			continue
		}
		messages = append(messages, record)
	}
	return messages, nil
}

// runForgeCheckCommits makes HISS-14 executable: every breaking commit in the range must
// carry a Migration: footer, otherwise the gate exits non-zero.
func runForgeCheckCommits(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forge check-commits", flag.ContinueOnError)
	base := fs.String("base", "", "Base revision of the range to analyze (required)")
	head := fs.String("head", "HEAD", "Head revision of the range to analyze")
	repoDir := fs.String("repo", ".", "Repository directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		return errors.New("usage: standardsctl forge check-commits --base=<ref> [--head=HEAD]")
	}

	messages, err := commitRange(ctx, *repoDir, *base, *head)
	if err != nil {
		return err
	}

	violations := 0
	for i := 0; i < len(messages); i++ {
		sha, message, _ := strings.Cut(messages[i], commitFieldSep)
		analysis, err := forge.AnalyzeCommit(message)
		if err != nil {
			return fmt.Errorf("failed analyzing commit %s: %w", sha, err)
		}
		if analysis.Valid {
			continue
		}
		violations++
		fmt.Printf("[FAIL] %s: %s\n", shortSHA(sha), strings.Join(analysis.Errors, "; "))
	}

	fmt.Printf("=== HISS-14 Commit Analysis: %d commit(s) in %s..%s ===\n", len(messages), *base, *head)
	if violations > 0 {
		return fmt.Errorf("HISS-14 violation: %d commit(s) declare a breaking change without a Migration: footer", violations)
	}
	fmt.Println("[PASS] Every breaking commit carries a mandatory Migration: footer.")
	return nil
}

func shortSHA(sha string) string {
	trimmed := strings.TrimSpace(sha)
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
}
