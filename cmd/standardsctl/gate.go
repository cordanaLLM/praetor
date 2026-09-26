package main

import (
	"context"
	"crypto/ed25519"
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

// gateQueryTimeout bounds the short git queries used by `gate verify`.
const gateQueryTimeout = 15 * time.Second

// gatedPipeline runs the gating pipeline. Tests substitute it to observe the deadline `gate run`
// hands the pipeline without running a real gate.
var gatedPipeline = gating.RunGatedPipeline

// runGate dispatches the gate subcommands: run (default), verify, deadline and keygen.
func runGate(args []string) error {
	sub, rest := splitGateSubcommand(args)
	switch sub {
	case "run":
		return runGateRun(rest)
	case "verify":
		return runGateVerify(rest)
	case "deadline":
		return runGateDeadline(rest)
	case "keygen":
		return runGateKeygen(rest)
	default:
		return fmt.Errorf("unknown gate subcommand %q (expected run, verify, deadline or keygen)", sub)
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
	// Flag parsing stops at the first positional argument; refusing leftovers keeps a
	// misplaced --path or --dry-run from being silently ignored.
	if fs.NArg() > 0 {
		return fmt.Errorf("gate run accepts no positional arguments, got %q (flags must precede them)", fs.Args())
	}

	// The run deadline follows the race stage's resolved bound. A fixed five minutes cut the
	// stage short whatever PRAETOR_TEST_STAGE_TIMEOUT asked for (#314).
	budget := gating.EnvRunBudget()
	ctx, cancel := gating.WithRunDeadline(context.Background(), budget)
	defer cancel()

	fmt.Printf("=== Praetor Anti-Direct-Merge Gating Pipeline ===\n")
	fmt.Printf("Target Repository: %s (dry-run: %v)\n", *path, *dryRun)
	fmt.Printf("Run Deadline: %s\n", budget)

	rep, err := gatedPipeline(ctx, *path, *dryRun)
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

// gateDeadlineReport is the machine-readable form of `gate deadline --json`.
type gateDeadlineReport struct {
	TimeoutSeconds       int64  `json:"timeout_seconds"`
	Timeout              string `json:"timeout"`
	StageBound           string `json:"stage_bound"`
	OtherStagesAllowance string `json:"other_stages_allowance"`
	Note                 string `json:"note,omitempty"`
}

// runGateDeadline prints the run deadline `gate run` applies in this environment, resolved from
// PRAETOR_TEST_STAGE_TIMEOUT by the pipeline's own parser. The pre-push hook bounds its gate
// subprocess by this value instead of a figure of its own, so the two cannot drift apart (#314).
func runGateDeadline(args []string) error {
	fs := flag.NewFlagSet("gate deadline", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Output the resolved run deadline as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("gate deadline accepts no positional arguments, got %q", fs.Args())
	}

	budget := gating.EnvRunBudget()
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(gateDeadlineReport{
			TimeoutSeconds:       budget.TimeoutSeconds(),
			Timeout:              budget.Timeout().String(),
			StageBound:           budget.StageBound.String(),
			OtherStagesAllowance: budget.Allowance.String(),
			Note:                 budget.Note,
		}); err != nil {
			return fmt.Errorf("encode gate deadline: %w", err)
		}
		return nil
	}
	fmt.Printf("Run Deadline: %s\n", budget)
	if budget.Note != "" {
		fmt.Printf("Note: %s\n", budget.Note)
	}
	return nil
}

// runGateVerify verifies an Exit-0 receipt against a public key: either the key pinned in
// .standards.yaml, or, with --public-key, a supplied Ed25519 key that always wins over any
// pinned key. It requires the receipt's commit_sha to equal --path's HEAD, and, when a key is
// supplied explicitly, additionally requires the receipt's repository to equal --path's
// resolved identity, since a supplied key has no manifest binding it to one repository. The
// public key embedded in the receipt is never a trust anchor on its own.
func runGateVerify(args []string) error {
	fs := flag.NewFlagSet("gate verify", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to the repository the receipt belongs to")
	receiptPath := fs.String("receipt", "", "Path to the receipt (default <path>/"+gating.ReceiptFileName+")")
	manifestPath := fs.String("config", "", "Path to .standards.yaml carrying receipt.public_key")
	publicKeyHex := fs.String("public-key", "",
		"Hex-encoded Ed25519 public key to verify against, overriding the manifest-pinned key")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("gate verify accepts no positional arguments, got %q", fs.Args())
	}

	resolvedReceipt := *receiptPath
	if resolvedReceipt == "" {
		resolvedReceipt = filepath.Join(*path, gating.ReceiptFileName)
	}

	rf, err := lockdown.LoadReceiptFile(resolvedReceipt)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}

	supplied := strings.TrimSpace(*publicKeyHex)
	pinned, err := resolveVerifyKey(supplied, *path, *manifestPath)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	if err := lockdown.VerifyPinnedReceipt(&rf.ExecutionReceipt, pinned, []byte(rf.GateOutput)); err != nil {
		return fmt.Errorf("[FAIL] receipt %s is not valid: %w", resolvedReceipt, err)
	}
	if err := verifyReceiptCommit(*path, rf.CommitSHA); err != nil {
		return err
	}
	if supplied != "" {
		if err := verifyReceiptRepository(*path, rf.Repository); err != nil {
			return err
		}
	}

	fmt.Printf("[PASS] Exit-0 receipt %s verified.\n", resolvedReceipt)
	fmt.Printf("       Command:    %s\n", rf.Command)
	fmt.Printf("       Repository: %s\n", rf.Repository)
	fmt.Printf("       Commit:     %s\n", rf.CommitSHA)
	if supplied != "" {
		fmt.Printf("       Signed by supplied key %s\n", hex.EncodeToString(pinned))
		return nil
	}
	fmt.Printf("       Signed by pinned key %s\n", hex.EncodeToString(pinned))
	return nil
}

// resolveVerifyKey returns the supplied hex-decoded Ed25519 public key when one is given. An
// explicit --public-key always wins over a manifest-pinned key: the operator asked to trust
// this specific key, not to cross-check it against .standards.yaml. With no supplied key it
// falls back to the existing manifest-pinned lookup, unchanged from before --public-key existed.
func resolveVerifyKey(supplied, path, manifestPath string) (ed25519.PublicKey, error) {
	if supplied != "" {
		return lockdown.ParsePinnedPublicKey(supplied)
	}
	resolvedManifest := manifestPath
	if resolvedManifest == "" {
		resolvedManifest = filepath.Join(path, ".standards.yaml")
	}
	return lockdown.PinnedPublicKey(resolvedManifest)
}

// verifyReceiptCommit binds a receipt to the commit currently checked out, through the same
// lockdown.VerifyReceiptCommit that paperclip verify uses, under the gate query timeout.
func verifyReceiptCommit(repoPath, receiptSHA string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gateQueryTimeout)
	defer cancel()

	if err := lockdown.VerifyReceiptCommit(ctx, repoPath, receiptSHA); err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	return nil
}

// verifyReceiptRepository binds a --public-key verified receipt to the repository identity
// resolved from repoPath, so a receipt minted for one repository cannot be replayed against
// another. It fails closed rather than guessing from a directory name: a receipt trusted
// through a supplied key has no manifest to cross-check the repository against.
func verifyReceiptRepository(repoPath, receiptRepository string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gateQueryTimeout)
	defer cancel()

	owner, repo, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("[FAIL] cannot resolve repository identity for %s: %w", repoPath, err)
	}
	expected := owner + "/" + repo
	if receiptRepository != expected {
		return fmt.Errorf("[FAIL] receipt attests repository %s but %s resolves to %s",
			receiptRepository, repoPath, expected)
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
	if fs.NArg() > 0 {
		return fmt.Errorf("gate keygen accepts no positional arguments, got %q", fs.Args())
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
