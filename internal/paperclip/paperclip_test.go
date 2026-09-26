package paperclip

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// receiptOutput is the gate output every fixture receipt certifies.
const receiptOutput = "praetor-gate-output/v1\nok\n"

// signedEnvelope returns a receipt envelope signed by priv that certifies receiptOutput.
func signedEnvelope(t *testing.T, priv ed25519.PrivateKey) *lockdown.ReceiptFile {
	t.Helper()
	return signedEnvelopeFor(t, priv, "commit1")
}

// signedEnvelopeFor returns a receipt envelope signed by priv that attests commit.
func signedEnvelopeFor(t *testing.T, priv ed25519.PrivateKey, commit string) *lockdown.ReceiptFile {
	t.Helper()
	receipt, err := lockdown.CreateReceipt("make verify-all", 0, []byte(receiptOutput), commit, "repo1", priv)
	if err != nil {
		t.Fatalf("create receipt failed: %v", err)
	}
	return &lockdown.ReceiptFile{ExecutionReceipt: *receipt, GateOutput: receiptOutput}
}

// keyPair returns a fresh Ed25519 key pair.
func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair failed: %v", err)
	}
	return pub, priv
}

// fixtureRepo is a hermetic git repository with a committed harness. Its branch tracks a
// bare remote on the local file system, so push checks run without a network.
type fixtureRepo struct {
	ctx    context.Context
	dir    string
	remote string
}

// newPushedRepo builds a fixtureRepo whose HEAD is pushed to its origin upstream. Its git
// commands run under testsupport.HermeticGitEnv, so the operator's hooks, signing setting and
// default branch cannot shape the fixture.
func newPushedRepo(t *testing.T) fixtureRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("hermetic fixture environment: %v", err)
	}
	repo := fixtureRepo{ctx: ctx, dir: t.TempDir(), remote: t.TempDir()}
	harness, err := SynthesizeHarness(ctx, repo.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(harness, repo.dir); err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, repo.remote, "init", "--quiet", "--bare"); err != nil {
		t.Skipf("git init --bare failed in sandbox: %v: %s", err, out)
	}
	// The AGit push of the harness protocol carries a push option, which a bare remote only
	// accepts when it advertises them.
	if out, err := util.RunGit(ctx, repo.remote, "config", "receive.advertisePushOptions", "true"); err != nil {
		t.Fatalf("enable push options on fixture remote: %v: %s", err, out)
	}
	repo.git(t, "init", "--quiet", "-b", "work")
	repo.git(t, "add", ".paperclip")
	repo.git(t, "commit", "--quiet", "-m", "test harness")
	repo.git(t, "remote", "add", "origin", repo.remote)
	repo.git(t, "push", "--quiet", "-u", "origin", "HEAD")
	return repo
}

// head returns the fixture's HEAD commit.
func (r fixtureRepo) head(t *testing.T) string {
	t.Helper()
	out, err := util.RunGit(r.ctx, r.dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v: %s", err, out)
	}
	return out
}

// runPushProtocol runs the harness push protocol for issueID, one git command per step, and
// stops after steps commands so a test can run only the AGit push.
func (r fixtureRepo) runPushProtocol(t *testing.T, issueID string, steps int) {
	t.Helper()
	commands := strings.Split(agitPushFormat, " && ")
	if steps > len(commands) {
		t.Fatalf("push protocol has %d steps, asked for %d", len(commands), steps)
	}
	for _, command := range commands[:steps] {
		fields := strings.Fields(strings.ReplaceAll(command, "<issue-id>", issueID))
		if len(fields) < 2 || fields[0] != "git" {
			t.Fatalf("push protocol step %q is not a git command", command)
		}
		r.git(t, fields[1:]...)
	}
}

// git runs one fixture git command and fails the test on error.
func (r fixtureRepo) git(t *testing.T, args ...string) {
	t.Helper()
	if out, err := util.RunGit(r.ctx, r.dir, args...); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// commitLocal records a local commit that is not pushed.
func (r fixtureRepo) commitLocal(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "local.txt"), []byte("local work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.git(t, "add", "local.txt")
	r.git(t, "commit", "--quiet", "-m", "local only")
}

func inReviewDisposition(t *testing.T) *Disposition {
	t.Helper()
	disposition, err := CreateDisposition("ISSUE-1", "in_review", "review", "PR proof", "", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	return disposition
}

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestDisposition_Positive_InReview(t *testing.T) {
	ctx := context.Background()
	pub, priv := keyPair(t)

	disp, err := CreateDisposition(
		"ISSUE-42",
		"in_review",
		"Implemented telemetry adapter with zero warnings",
		"PR #123 opened, 100% 3D tests passed",
		"",
		"agent-1",
		signedEnvelope(t, priv),
	)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if err := disp.Validate(ctx, pub); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if disp.Status != StatusInReview {
		t.Fatalf("expected in_review status, got: %s", disp.Status)
	}

	jsonBytes, err := disp.FormatJSON()
	if err != nil || len(jsonBytes) == 0 {
		t.Fatal("FormatJSON failed")
	}
	if !strings.Contains(string(jsonBytes), `"gate_output"`) {
		t.Fatalf("receipt envelope must serialize its gate output, got %s", jsonBytes)
	}
}

func TestDisposition_Positive_Blocked(t *testing.T) {
	ctx := context.Background()
	disp, err := CreateDisposition(
		"ISSUE-99",
		"blocked",
		"Missing upstream dependency auth token",
		"",
		"infra-team",
		"agent-2",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if err := disp.Validate(ctx, nil); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if disp.Status != StatusBlocked {
		t.Fatalf("expected blocked status, got: %s", disp.Status)
	}
}

func TestHarness_Positive_SynthesizeAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := SynthesizeHarness(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SynthesizeHarness failed: %v", err)
	}

	if err := WriteHarness(h, tmpDir); err != nil {
		t.Fatalf("WriteHarness failed: %v", err)
	}

	jsonPath := filepath.Join(tmpDir, ".paperclip", "harness.json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", jsonPath)
	}

	mdPath := filepath.Join(tmpDir, ".paperclip", "rules.md")
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", mdPath)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestDisposition_Negative_MissingFields(t *testing.T) {
	// Missing issue ID
	_, err := CreateDisposition("", "in_review", "note", "proof", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty issueID")
	}

	// InReview missing proof
	_, err = CreateDisposition("ISSUE-1", "in_review", "note", "", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty proof for in_review")
	}

	// Blocked missing recovery owner
	_, err = CreateDisposition("ISSUE-1", "blocked", "note", "", "", "actor", nil)
	if err == nil {
		t.Fatal("expected error on empty recovery owner for blocked")
	}

	// Nil context in validation
	disp, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "owner", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	if err := disp.Validate(nilContext, nil); err == nil {
		t.Fatal("expected error on nil context in Validate")
	}
}

func TestDisposition_Negative_TamperedReceipt(t *testing.T) {
	ctx := context.Background()
	pub, priv := keyPair(t)
	envelope := signedEnvelope(t, priv)

	// Tamper with receipt
	envelope.ExitCode = 1

	disp, err := CreateDisposition("ISSUE-1", "in_review", "note", "proof", "", "actor", envelope)
	if err != nil {
		t.Fatal(err)
	}

	if err := disp.Validate(ctx, pub); err == nil {
		t.Fatal("expected validation error on tampered receipt")
	}
}

// TestDisposition_ReceiptVerifiedAgainstPinnedKey covers receipt trust: only the pinned key
// is a trust anchor, and the envelope's gate output must match the signed hash.
func TestDisposition_ReceiptVerifiedAgainstPinnedKey(t *testing.T) {
	ctx := context.Background()
	pub, priv := keyPair(t)
	_, foreignPriv := keyPair(t)

	// Positive: signed by the pinned key over the carried output.
	pinned, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "owner", "actor", signedEnvelope(t, priv))
	if err != nil {
		t.Fatal(err)
	}
	if err := pinned.Validate(ctx, pub); err != nil {
		t.Fatalf("pinned-key receipt rejected: %v", err)
	}

	// Negative: a self-consistent receipt signed by a foreign key is not trusted.
	foreign, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "owner", "actor", signedEnvelope(t, foreignPriv))
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.Validate(ctx, pub); !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Fatalf("foreign-key receipt must fail with ErrKeyNotPinned, got %v", err)
	}

	// Negative: a receipt cannot be verified without a pinned key.
	if err := pinned.Validate(ctx, nil); !errors.Is(err, lockdown.ErrNoPinnedKey) {
		t.Fatalf("receipt without pinned key must fail with ErrNoPinnedKey, got %v", err)
	}

	// Negative: gate output that no longer matches the signed hash.
	pinned.Receipt.GateOutput = "praetor-gate-output/v1\nforged\n"
	if err := pinned.Validate(ctx, pub); !errors.Is(err, lockdown.ErrOutputMismatch) {
		t.Fatalf("mismatched gate output must fail with ErrOutputMismatch, got %v", err)
	}

	// Boundary: without a receipt no key is needed.
	bare, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "owner", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bare.Validate(ctx, nil); err != nil {
		t.Fatalf("receipt-less disposition must not need a pinned key: %v", err)
	}
}

// TestDisposition_ValidateRejectsWhitespaceFields checks that a directly constructed or
// decoded record gets the same whitespace rules CreateDisposition applies.
func TestDisposition_ValidateRejectsWhitespaceFields(t *testing.T) {
	ctx := context.Background()
	cases := map[string]Disposition{
		"issue":          {IssueID: " \t", Status: StatusBlocked, Note: "n", RecoveryOwner: "o"},
		"note":           {IssueID: "I-1", Status: StatusBlocked, Note: "  \n", RecoveryOwner: "o"},
		"proof":          {IssueID: "I-1", Status: StatusInReview, Note: "n", Proof: " "},
		"recovery_owner": {IssueID: "I-1", Status: StatusBlocked, Note: "n", RecoveryOwner: "\t"},
	}
	for field, disposition := range cases {
		t.Run(field, func(t *testing.T) {
			if err := disposition.Validate(ctx, nil); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("whitespace-only %s must be rejected, got %v", field, err)
			}
			_, err := CreateDisposition(disposition.IssueID, string(disposition.Status), disposition.Note,
				disposition.Proof, disposition.RecoveryOwner, "actor", nil)
			if err == nil {
				t.Fatalf("CreateDisposition must reject whitespace-only %s too", field)
			}
		})
	}

	// Boundary: surrounding whitespace around real content is accepted.
	padded := Disposition{IssueID: " I-1 ", Status: StatusBlocked, Note: " note ", RecoveryOwner: " owner "}
	if err := padded.Validate(ctx, nil); err != nil {
		t.Fatalf("padded non-empty fields must be accepted: %v", err)
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestDisposition_Boundary_DoneMapsToInReview(t *testing.T) {
	// ADR-0087: "done" must automatically map to "in_review" because agent claims require review
	disp, err := CreateDisposition("ISSUE-1", "DONE", "finished", "proof of pass", "", "actor", nil)
	if err != nil {
		t.Fatalf("CreateDisposition failed: %v", err)
	}

	if disp.Status != StatusInReview {
		t.Fatalf("expected DONE to map to in_review, got: %s", disp.Status)
	}
}

// TestDisposition_Boundary_StatusCaseConsistency pins that CreateDisposition and Validate
// accept and reject exactly the same raw status spellings, and that Validate leaves an
// accepted record holding the canonical status and a rejected one untouched.
func TestDisposition_Boundary_StatusCaseConsistency(t *testing.T) {
	ctx := context.Background()
	for raw, want := range map[string]DispositionStatus{
		"in_review": StatusInReview, "IN_REVIEW": StatusInReview, " In_Review\t": StatusInReview,
		"blocked": StatusBlocked, "Blocked": StatusBlocked, "done": StatusInReview, " DONE ": StatusInReview,
		"shipped": "", "": "", "in review": "", "in_review_": "",
	} {
		t.Run(raw, func(t *testing.T) {
			got, parseErr := ParseStatus(raw)
			if got != want || (parseErr == nil) != (want != "") {
				t.Fatalf("ParseStatus(%q) = %q, %v; want %q", raw, got, parseErr, want)
			}
			_, createErr := CreateDisposition("I-1", raw, "note", "proof", "owner", "actor", nil)
			direct := Disposition{IssueID: "I-1", Status: DispositionStatus(raw), Note: "note", Proof: "proof", RecoveryOwner: "owner"}
			validateErr := direct.Validate(ctx, nil)
			if (createErr == nil) != (validateErr == nil) {
				t.Fatalf("status %q: CreateDisposition err=%v but Validate err=%v", raw, createErr, validateErr)
			}
			if (validateErr == nil) != (want != "") {
				t.Fatalf("status %q: Validate err=%v, want accepted=%t", raw, validateErr, want != "")
			}
			if validateErr == nil && direct.Status != want {
				t.Fatalf("status %q: Validate left Status %q, want canonical %q", raw, direct.Status, want)
			}
			if validateErr != nil && direct.Status != DispositionStatus(raw) {
				t.Fatalf("status %q: rejected record Status rewritten to %q", raw, direct.Status)
			}
		})
	}
}

func TestVerifyRun_Boundary_MissingHarness(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	disp, err := CreateDisposition("ISSUE-1", "blocked", "stuck", "", "ops", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyRun(ctx, tmpDir, disp, VerifyOptions{})
	if err == nil {
		t.Fatal("expected error when .paperclip/harness.json is missing")
	}
}

func TestReadDisposition_3D(t *testing.T) {
	tmpDir := t.TempDir()
	dispPath := filepath.Join(tmpDir, "disp.json")

	// Boundary: Non-existent file
	if _, err := ReadDisposition(filepath.Join(tmpDir, "nope.json")); err == nil {
		t.Fatal("expected error reading non-existent file")
	}

	// Negative: Corrupt JSON
	if err := os.WriteFile(dispPath, []byte("invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDisposition(dispPath); err == nil {
		t.Fatal("expected error parsing corrupt json")
	}

	// Positive: Valid disposition
	disp, err := CreateDisposition("ISSUE-10", "blocked", "need key", "", "security", "bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := disp.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dispPath, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadDisposition(dispPath)
	if err != nil || loaded.IssueID != "ISSUE-10" {
		t.Fatalf("failed to read valid disposition: %v", err)
	}
}

func TestLoadHarness_3D(t *testing.T) {
	tmpDir := t.TempDir()
	harnessPath := filepath.Join(tmpDir, "harness.json")

	// Boundary: Non-existent file
	if _, err := LoadHarness(filepath.Join(tmpDir, "missing.json")); err == nil {
		t.Fatal("expected error reading non-existent file")
	}

	// Negative: Corrupt JSON
	if err := os.WriteFile(harnessPath, []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarness(harnessPath); err == nil {
		t.Fatal("expected error parsing corrupt json")
	}

	// Negative: Missing platform
	if err := os.WriteFile(harnessPath, []byte(`{"version":1,"operating_contract":["rule1"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarness(harnessPath); err == nil {
		t.Fatal("expected error with missing platform")
	}

	// Positive: Synthesize and load
	h, err := SynthesizeHarness(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("SynthesizeHarness failed: %v", err)
	}
	if err := WriteHarness(h, tmpDir); err != nil {
		t.Fatalf("WriteHarness failed: %v", err)
	}
	loaded, err := LoadHarness(filepath.Join(tmpDir, ".paperclip", "harness.json"))
	if err != nil || loaded.Platform != h.Platform {
		t.Fatalf("LoadHarness failed or platform mismatch: %v (got %s, expected %s)", err, loaded.Platform, h.Platform)
	}

	// Test with explicit manifest
	manifestDir := t.TempDir()
	manifestContent := "repository:\n  owner: test-org\n  name: test-repo\n"
	if err := os.WriteFile(filepath.Join(manifestDir, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}
	h2, err := SynthesizeHarness(context.Background(), manifestDir)
	if err != nil || h2.Platform != "test-org/test-repo" {
		t.Fatalf("expected platform 'test-org/test-repo', got: %s (err: %v)", h2.Platform, err)
	}
}

func TestVerifyRunInReviewWorkingTree(t *testing.T) {
	disposition := inReviewDisposition(t)
	if err := VerifyRun(t.Context(), t.TempDir(), disposition, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "git status") {
		t.Fatalf("nonrepository must report git failure, got %v", err)
	}
	repo := newPushedRepo(t)
	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
		t.Fatalf("clean pushed worktree rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo.dir, "unfinished.txt"), []byte("work in progress"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty worktree must fail disposition, got %v", err)
	}
	cancelled, cancel := context.WithCancel(repo.ctx)
	cancel()
	if err := VerifyRun(cancelled, repo.dir, disposition, VerifyOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification must preserve context error, got %v", err)
	}
}

// TestVerifyRun_InReviewRequiresPushedHead covers "pushing is not shipping": an in_review
// run must at least have pushed HEAD, which a remote-tracking ref containing it proves locally.
func TestVerifyRun_InReviewRequiresPushedHead(t *testing.T) {
	disposition := inReviewDisposition(t)
	notPushed := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "HEAD is not pushed") {
			t.Fatalf("unpushed HEAD must fail, got %v", err)
		}
	}

	t.Run("unpushed commit", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		notPushed(t, VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}))
		repo.git(t, "push", "--quiet")
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("HEAD pushed after the commit must pass: %v", err)
		}
	})
	t.Run("only local refs contain HEAD", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		repo.git(t, "branch", "local-copy")
		repo.git(t, "branch", "--set-upstream-to=local-copy")
		notPushed(t, VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}))
	})
	t.Run("no remote-tracking refs", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.git(t, "remote", "remove", "origin")
		notPushed(t, VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}))
	})
	t.Run("pushed without upstream config", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.git(t, "branch", "--unset-upstream")
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("a pushed HEAD needs no upstream configuration: %v", err)
		}
	})
	t.Run("detached head", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.git(t, "checkout", "--quiet", "--detach")
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("a detached HEAD on a pushed commit must pass: %v", err)
		}
		repo.commitLocal(t)
		notPushed(t, VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}))
	})
	t.Run("mixed-case status still checked", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		direct := &Disposition{IssueID: "I-1", Status: "IN_REVIEW", Note: "n", Proof: "p"}
		notPushed(t, VerifyRun(repo.ctx, repo.dir, direct, VerifyOptions{}))
	})
	t.Run("blocked skips push", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		blocked, err := CreateDisposition("ISSUE-1", "blocked", "stuck", "", "ops", "actor", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyRun(repo.ctx, repo.dir, blocked, VerifyOptions{}); err != nil {
			t.Fatalf("blocked disposition must not require a push: %v", err)
		}
	})
}

// TestVerifyRun_HarnessPushProtocolSatisfiesVerify runs the push protocol the synthesized
// harness prescribes and checks VerifyRun accepts its result, so harness, rules.md and verify
// cannot drift apart again.
func TestVerifyRun_HarnessPushProtocolSatisfiesVerify(t *testing.T) {
	disposition := inReviewDisposition(t)

	t.Run("full protocol", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		repo.runPushProtocol(t, "ISSUE-1", 2)
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("the harness push protocol must satisfy verify: %v", err)
		}
	})
	t.Run("from local main", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.git(t, "checkout", "--quiet", "-b", "main")
		repo.commitLocal(t)
		repo.runPushProtocol(t, "ISSUE-2", 2)
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("the harness push protocol must satisfy verify from main: %v", err)
		}
		if out, err := util.RunGit(repo.ctx, repo.remote, "rev-parse", "--verify", "--quiet", "refs/heads/main"); err == nil {
			t.Fatalf("the push protocol must never push to the remote main, found %s", out)
		}
	})
	t.Run("AGit push alone", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		repo.runPushProtocol(t, "ISSUE-3", 1)
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "HEAD is not pushed") {
			t.Fatalf("an AGit push records no local ref and must not count as pushed, got %v", err)
		}
	})
	t.Run("fetched review ref", func(t *testing.T) {
		repo := newPushedRepo(t)
		repo.commitLocal(t)
		// Stands in for the head ref a forge creates for the review opened by the AGit push.
		repo.git(t, "push", "--quiet", "origin", "HEAD:refs/pull/1/head")
		repo.git(t, "fetch", "--quiet", "origin", "+refs/pull/1/head:refs/remotes/origin/pull/1")
		if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err != nil {
			t.Fatalf("a fetched review ref must count as pushed: %v", err)
		}
	})
}

// TestVerifyRun_DispositionFileExcluded covers the documented workflow: the disposition is
// written to .paperclip/disposition.json after the push, and only that file may be dirty.
func TestVerifyRun_DispositionFileExcluded(t *testing.T) {
	disposition := inReviewDisposition(t)
	repo := newPushedRepo(t)
	data, err := disposition.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	dispositionPath := filepath.Join(repo.dir, ".paperclip", "disposition.json")
	if err := os.WriteFile(dispositionPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{DispositionPath: dispositionPath}); err != nil {
		t.Fatalf("the untracked disposition file itself must not count as uncommitted work: %v", err)
	}
	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("an unnamed disposition file is ordinary untracked work, got %v", err)
	}
	outside := filepath.Join(t.TempDir(), "disposition.json")
	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{DispositionPath: outside}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("a path outside the repository must exclude nothing, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo.dir, ".paperclip", "notes.txt"), []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{DispositionPath: dispositionPath}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("other untracked files beside the disposition must still fail, got %v", err)
	}
}

// TestVerifyRun_GateReceiptFileExcluded covers the documented in_review order with a receipt:
// commit, push, mint the receipt with `praetorctl gate run` (written to the repository root),
// then write the disposition. The receipt file on disk is gate output, not unpushed work.
func TestVerifyRun_GateReceiptFileExcluded(t *testing.T) {
	repo := newPushedRepo(t)
	pub, priv := keyPair(t)
	envelope := signedEnvelopeFor(t, priv, repo.head(t))
	disposition, err := CreateDisposition("ISSUE-1", "in_review", "review", "PR proof", "", "actor", envelope)
	if err != nil {
		t.Fatal(err)
	}
	dispositionPath := filepath.Join(repo.dir, ".paperclip", "disposition.json")
	data, err := disposition.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dispositionPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(repo.dir, gating.ReceiptFileName)
	if err := lockdown.SaveReceiptFile(receiptPath, envelope, gating.ReceiptFilePerm); err != nil {
		t.Fatal(err)
	}
	opts := VerifyOptions{PinnedKey: pub, DispositionPath: dispositionPath}
	verify := func() error { return VerifyRun(repo.ctx, repo.dir, disposition, opts) }

	// Positive: the untracked receipt the gate just wrote beside the disposition.
	if err := verify(); err != nil {
		t.Fatalf("the untracked gate receipt must not count as uncommitted work: %v", err)
	}
	// Boundary: a repository that tracks its receipt sees it modified by the next gate run.
	repo.git(t, "add", gating.ReceiptFileName)
	repo.git(t, "commit", "--quiet", "-m", "track receipt")
	repo.runPushProtocol(t, "ISSUE-1", 2)
	envelope = signedEnvelopeFor(t, priv, repo.head(t))
	disposition.Receipt = envelope
	if err := lockdown.SaveReceiptFile(receiptPath, envelope, gating.ReceiptFilePerm); err != nil {
		t.Fatal(err)
	}
	if status, err := util.RunGit(repo.ctx, repo.dir, "status", "--porcelain", "--", gating.ReceiptFileName); err != nil || !strings.HasPrefix(status, "M") {
		t.Fatalf("fixture receipt must be tracked and modified, got %q (%v)", status, err)
	}
	if err := verify(); err != nil {
		t.Fatalf("a modified tracked gate receipt must not count as uncommitted work: %v", err)
	}
	// Negative: only the root receipt is exempt; a same-named file elsewhere or a near-miss
	// name at the root is ordinary untracked work.
	for _, rel := range []string{filepath.Join("sub", gating.ReceiptFileName), gating.ReceiptFileName + ".bak"} {
		t.Run(rel, func(t *testing.T) {
			stray := filepath.Join(repo.dir, rel)
			if err := os.MkdirAll(filepath.Dir(stray), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stray, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(stray); err != nil {
					t.Errorf("remove %s: %v", rel, err)
				}
			})
			if err := verify(); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
				t.Fatalf("%s must still fail the clean-tree check, got %v", rel, err)
			}
		})
	}
}

func TestDispositionPathspec_3D(t *testing.T) {
	root := t.TempDir()
	if got, ok := dispositionPathspec(root, filepath.Join(root, ".paperclip", "disposition.json")); !ok || got != ":(exclude,literal).paperclip/disposition.json" {
		t.Fatalf("in-repository path = %q, %t", got, ok)
	}
	for name, path := range map[string]string{
		"empty":        "",
		"root itself":  root,
		"outside":      filepath.Join(t.TempDir(), "disposition.json"),
		"parent climb": filepath.Join(root, "..", "disposition.json"),
	} {
		if got, ok := dispositionPathspec(root, path); ok {
			t.Errorf("%s: expected no exclusion, got %q", name, got)
		}
	}
}

// TestVerifyRun_PinnedKeyThreaded checks that VerifyRun verifies an attached receipt against
// opts.PinnedKey rather than the key embedded in the receipt.
func TestVerifyRun_PinnedKeyThreaded(t *testing.T) {
	repo := newPushedRepo(t)
	head := repo.head(t)
	pub, priv := keyPair(t)
	_, foreignPriv := keyPair(t)
	for name, tc := range map[string]struct {
		priv ed25519.PrivateKey
		pin  ed25519.PublicKey
		want error
	}{
		"pinned":   {priv: priv, pin: pub},
		"foreign":  {priv: foreignPriv, pin: pub, want: lockdown.ErrKeyNotPinned},
		"unpinned": {priv: priv, want: lockdown.ErrNoPinnedKey},
	} {
		t.Run(name, func(t *testing.T) {
			disposition, err := CreateDisposition("ISSUE-1", "blocked", "stuck", "", "ops", "actor", signedEnvelopeFor(t, tc.priv, head))
			if err != nil {
				t.Fatal(err)
			}
			err = VerifyRun(repo.ctx, repo.dir, disposition, VerifyOptions{PinnedKey: tc.pin})
			if tc.want == nil && err != nil {
				t.Fatalf("pinned receipt rejected: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

// TestVerifyRun_ReceiptBoundToHead checks that a receipt signed by the pinned key only verifies
// for the commit it attests, so an older receipt cannot be replayed on a later disposition.
func TestVerifyRun_ReceiptBoundToHead(t *testing.T) {
	repo := newPushedRepo(t)
	pub, priv := keyPair(t)
	earlier := repo.head(t)
	repo.commitLocal(t)
	repo.git(t, "push", "--quiet")
	head := repo.head(t)
	opts := VerifyOptions{PinnedKey: pub}
	verify := func(t *testing.T, status, commit string) error {
		t.Helper()
		disposition, err := CreateDisposition("ISSUE-1", status, "note", "PR proof", "ops", "actor", signedEnvelopeFor(t, priv, commit))
		if err != nil {
			t.Fatal(err)
		}
		return VerifyRun(repo.ctx, repo.dir, disposition, opts)
	}

	// Positive: the receipt attests HEAD, for either status.
	for _, status := range []string{"in_review", "blocked"} {
		if err := verify(t, status, head); err != nil {
			t.Fatalf("%s receipt for HEAD rejected: %v", status, err)
		}
	}
	// Negative: a validly signed receipt for an earlier commit, for either status.
	for _, status := range []string{"in_review", "blocked"} {
		if err := verify(t, status, earlier); !errors.Is(err, lockdown.ErrCommitMismatch) {
			t.Fatalf("%s receipt for an earlier commit must fail with ErrCommitMismatch, got %v", status, err)
		}
	}
	// Boundary: a receipt minted without a commit, and an abbreviated HEAD, never match.
	for name, commit := range map[string]string{"empty": "", "abbreviated": head[:12]} {
		if err := verify(t, "blocked", commit); !errors.Is(err, lockdown.ErrCommitMismatch) {
			t.Fatalf("%s receipt commit must fail with ErrCommitMismatch, got %v", name, err)
		}
	}
	// Negative: outside a repository there is no HEAD to bind to.
	disposition, err := CreateDisposition("ISSUE-1", "blocked", "note", "", "ops", "actor", signedEnvelopeFor(t, priv, head))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRun(repo.ctx, t.TempDir(), disposition, opts); err == nil || !strings.Contains(err.Error(), "cannot resolve HEAD") {
		t.Fatalf("a receipt outside a repository must fail to bind, got %v", err)
	}
}
