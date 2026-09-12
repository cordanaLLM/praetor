package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// gateRunTimeout bounds a full gating pipeline run.
	gateRunTimeout = 5 * time.Minute
	// gateQueryTimeout bounds the short git queries used by `gate verify`.
	gateQueryTimeout = 15 * time.Second
)

// runGate dispatches the gate subcommands: run (default), verify and keygen.
func runGate(args []string) error {
	sub, rest := splitGateSubcommand(args)
	switch sub {
	case "run":
		return runGateRun(rest)
	case "verify":
		return runGateVerify(rest)
	case "keygen":
		return runGateKeygen(rest)
	default:
		return fmt.Errorf("unknown gate subcommand %q (expected run, verify or keygen)", sub)
	}
}

// splitGateSubcommand extracts a leading subcommand, defaulting to "run" so that
// `gate --path=.` keeps working.
func splitGateSubcommand(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "run", args
	}
	return args[0], args[1:]
}

func runGateRun(args []string) error {
	fs := flag.NewFlagSet("gate run", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to repository to verify against gating pipeline")
	dryRun := fs.Bool("dry-run", false, "Skip the race-detector test stage; no receipt is minted")
	asJSON := fs.Bool("json", false, "Output pipeline results as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), gateRunTimeout)
	defer cancel()

	fmt.Printf("=== Praetor Anti-Direct-Merge Gating Pipeline ===\n")
	fmt.Printf("Target Repository: %s (dry-run: %v)\n", *path, *dryRun)

	rep, err := gating.RunGatedPipeline(ctx, *path, *dryRun)
	if err != nil {
		return fmt.Errorf("gating pipeline execution failed: %w", err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(rep); encErr != nil {
			return fmt.Errorf("encode gating report: %w", encErr)
		}
		if rep.Status == gating.StatusRejected {
			return fmt.Errorf("repository rejected by gating pipeline")
		}
		return nil
	}

	printGatingReport(rep)
	if rep.Status == gating.StatusRejected {
		return fmt.Errorf("repository rejected by gating pipeline")
	}
	return nil
}

// runGateVerify verifies an Exit-0 receipt against the public key pinned in
// .standards.yaml and against the current HEAD commit. The public key embedded in the
// receipt is never a trust anchor on its own.
func runGateVerify(args []string) error {
	fs := flag.NewFlagSet("gate verify", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to the repository the receipt belongs to")
	receiptPath := fs.String("receipt", "", "Path to the receipt (default <path>/"+gating.ReceiptFileName+")")
	manifestPath := fs.String("config", "", "Path to .standards.yaml carrying receipt.public_key")

	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedReceipt := *receiptPath
	if resolvedReceipt == "" {
		resolvedReceipt = filepath.Join(*path, gating.ReceiptFileName)
	}
	resolvedManifest := *manifestPath
	if resolvedManifest == "" {
		resolvedManifest = filepath.Join(*path, ".standards.yaml")
	}

	rf, err := lockdown.LoadReceiptFile(resolvedReceipt)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	pinned, err := lockdown.PinnedPublicKey(resolvedManifest)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	if err := lockdown.VerifyPinnedReceipt(&rf.ExecutionReceipt, pinned, []byte(rf.GateOutput)); err != nil {
		return fmt.Errorf("[FAIL] receipt %s is not valid: %w", resolvedReceipt, err)
	}
	if err := verifyReceiptCommit(*path, rf.CommitSHA); err != nil {
		return err
	}

	fmt.Printf("[PASS] Exit-0 receipt %s verified.\n", resolvedReceipt)
	fmt.Printf("       Command:    %s\n", rf.Command)
	fmt.Printf("       Repository: %s\n", rf.Repository)
	fmt.Printf("       Commit:     %s\n", rf.CommitSHA)
	fmt.Printf("       Signed by pinned key %s\n", hex.EncodeToString(pinned))
	return nil
}

// verifyReceiptCommit binds a receipt to the commit currently checked out.
func verifyReceiptCommit(repoPath, receiptSHA string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gateQueryTimeout)
	defer cancel()

	head, err := util.RunGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("[FAIL] cannot resolve HEAD in %s: %w", repoPath, err)
	}
	head = strings.TrimSpace(head)
	if head != receiptSHA {
		return fmt.Errorf("[FAIL] receipt attests commit %s but HEAD is %s", receiptSHA, head)
	}
	return nil
}

// runGateKeygen creates the long-lived Ed25519 receipt signing key and prints the public
// half to pin in .standards.yaml.
func runGateKeygen(args []string) error {
	fs := flag.NewFlagSet("gate keygen", flag.ContinueOnError)
	keyPath := fs.String("key", "", "Where to write the private key (default ~/.config/praetor/receipt.key)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	resolved := *keyPath
	if resolved == "" {
		defaultPath, err := lockdown.DefaultSigningKeyPath()
		if err != nil {
			return err
		}
		resolved = defaultPath
	}

	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		return err
	}
	if err := lockdown.SaveSigningKey(resolved, priv); err != nil {
		return err
	}

	fmt.Printf("[CREATED] Ed25519 receipt signing key: %s (mode 0600)\n", resolved)
	fmt.Printf("\nPin the public half in .standards.yaml so gates can verify receipts:\n\n")
	fmt.Printf("receipt:\n  public_key: \"%s\"\n\n", hex.EncodeToString(pub))
	fmt.Printf("Alternatively export the private key as %s=<hex seed> in CI.\n", lockdown.SigningKeyEnv)
	return nil
}

func printGatingReport(rep *gating.PipelineReport) {
	fmt.Printf("\nPipeline Result: %s (total: %v)\n", rep.Status, rep.TotalElapsed.Round(time.Millisecond))
	fmt.Printf("Scanned: %s @ %s (worktree clean: %v)\n", rep.Repository, rep.CommitSHA, rep.WorktreeClean)
	for idx, s := range rep.Stages {
		statusStr := "[PASS]"
		if !s.Passed {
			statusStr = "[FAIL]"
		}
		fmt.Printf("  %d. %s %-25s (%v)\n", idx+1, statusStr, s.Name, s.Duration.Round(time.Millisecond))
		if s.Message != "" {
			fmt.Printf("     Reason: %s\n", s.Message)
		}
	}
	if rep.ReceiptSignature != "" {
		fmt.Printf("\nExit-0 Receipt: %s (Ed25519 signature: %s...)\n",
			rep.ReceiptPath, rep.ReceiptSignature[:16])
		fmt.Printf("Verify it with: praetorctl gate verify --path=%s\n", rep.RepoDir)
		return
	}
	if rep.DryRun {
		fmt.Printf("\nDry run: no Exit-0 receipt was minted (tests did not run).\n")
	}
}
