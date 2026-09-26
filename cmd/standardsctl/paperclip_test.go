package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

// writePaperclipFixtureHarness writes a valid .paperclip harness into dir.
func writePaperclipFixtureHarness(t *testing.T, dir string) {
	t.Helper()
	harness, err := paperclip.SynthesizeHarness(context.Background(), dir)
	if err != nil {
		t.Fatalf("SynthesizeHarness: %v", err)
	}
	if err := paperclip.WriteHarness(harness, dir); err != nil {
		t.Fatalf("WriteHarness: %v", err)
	}
}

// writeReceiptDisposition writes a blocked disposition to the default path, carrying a
// receipt envelope for HEAD signed by priv when priv is non-nil.
func (f *gateFixture) writeReceiptDisposition(t *testing.T, priv ed25519.PrivateKey) {
	t.Helper()
	f.writeReceiptDispositionFor(t, priv, f.head)
}

// writeReceiptDispositionFor is writeReceiptDisposition with a receipt attesting commit.
func (f *gateFixture) writeReceiptDispositionFor(t *testing.T, priv ed25519.PrivateKey, commit string) {
	t.Helper()
	var envelope *lockdown.ReceiptFile
	if priv != nil {
		receipt, err := lockdown.CreateReceipt("make verify-all", 0, f.output, commit, "acme/widget", priv)
		if err != nil {
			t.Fatalf("CreateReceipt: %v", err)
		}
		envelope = &lockdown.ReceiptFile{ExecutionReceipt: *receipt, GateOutput: string(f.output)}
	}
	f.writeDisposition(t, "blocked", envelope)
}

// writeDisposition writes a disposition with status and the optional receipt envelope to the
// default path. A blocked disposition names a recovery owner, an in_review one a proof.
func (f *gateFixture) writeDisposition(t *testing.T, status string, envelope *lockdown.ReceiptFile) {
	t.Helper()
	proof := ""
	if status == "in_review" {
		proof = "https://example.invalid/pull/7"
	}
	disposition, err := paperclip.CreateDisposition("ISSUE-7", status, "waiting on credentials", proof, "ops", "agent", envelope)
	if err != nil {
		t.Fatalf("CreateDisposition: %v", err)
	}
	data, err := disposition.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, ".paperclip", "disposition.json"), data, 0o600); err != nil {
		t.Fatalf("write disposition: %v", err)
	}
}

// TestPaperclipVerify_PinnedReceipt checks that `paperclip verify` trusts only the key pinned
// in .standards.yaml, never the key a disposition's receipt carries.
func TestPaperclipVerify_PinnedReceipt(t *testing.T) {
	f := newGateFixture(t)
	writePaperclipFixtureHarness(t, f.dir)
	_, foreignPriv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	verify := []string{"verify", "--path=" + f.dir}

	// Positive: receipt signed by the pinned key.
	f.pin(t, f.pub)
	f.writeReceiptDisposition(t, f.priv)
	if err := runPaperclip(verify); err != nil {
		t.Fatalf("pinned receipt rejected: %v", err)
	}

	// Negative: a self-consistent receipt signed by a foreign key.
	f.writeReceiptDisposition(t, foreignPriv)
	if err := runPaperclip(verify); !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Fatalf("foreign-key receipt must fail with ErrKeyNotPinned, got %v", err)
	}

	// Negative: a receipt the pinned key signed for another commit cannot be replayed.
	f.writeReceiptDispositionFor(t, f.priv, strings.Repeat("0", len(f.head)))
	if err := runPaperclip(verify); !errors.Is(err, lockdown.ErrCommitMismatch) {
		t.Fatalf("receipt for another commit must fail with ErrCommitMismatch, got %v", err)
	}

	// Negative: a receipt in a repository that pins no key.
	f.writeReceiptDisposition(t, f.priv)
	if err := os.WriteFile(filepath.Join(f.dir, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := runPaperclip(verify); !errors.Is(err, lockdown.ErrNoPinnedKey) {
		t.Fatalf("receipt without a pinned key must fail with ErrNoPinnedKey, got %v", err)
	}

	// Boundary: --config names a manifest outside the repository that carries the pin.
	elsewhere := filepath.Join(t.TempDir(), "standards.yaml")
	body := "version: 1\nreceipt:\n  public_key: \"" + hex.EncodeToString(f.pub) + "\"\n"
	if err := os.WriteFile(elsewhere, []byte(body), 0o600); err != nil {
		t.Fatalf("write external manifest: %v", err)
	}
	if err := runPaperclip(append(verify, "--config="+elsewhere)); err != nil {
		t.Fatalf("--config manifest pin ignored: %v", err)
	}

	// Boundary: a receipt-less disposition needs no pinned key.
	f.writeReceiptDisposition(t, nil)
	if err := runPaperclip(verify); err != nil {
		t.Fatalf("receipt-less disposition must not need a pin: %v", err)
	}
}

// TestPaperclipVerify_InReviewDefaultDispositionPath runs the documented workflow end to end:
// push the branch, write the disposition to its default path, then verify.
func TestPaperclipVerify_InReviewDefaultDispositionPath(t *testing.T) {
	f := newGateFixture(t)
	writePaperclipFixtureHarness(t, f.dir)
	f.commitAndPush(t, ".paperclip")
	disposition := []string{
		"disposition", "--issue=ISSUE-8", "--status=in_review", "--note=done and pushed",
		"--proof=https://example.invalid/pull/8",
		"--output=" + filepath.Join(f.dir, ".paperclip", "disposition.json"),
	}
	if err := runPaperclip(disposition); err != nil {
		t.Fatalf("paperclip disposition: %v", err)
	}
	verify := []string{"verify", "--path=" + f.dir}
	if err := runPaperclip(verify); err != nil {
		t.Fatalf("pushed branch with its own default disposition file rejected: %v", err)
	}

	// Negative: a local commit that was never pushed.
	if err := os.WriteFile(filepath.Join(f.dir, "later.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatalf("write later.txt: %v", err)
	}
	for _, args := range [][]string{{"add", "later.txt"}, {"commit", "-q", "-m", "later"}} {
		if out, err := runFixtureGit(t, f.dir, f.env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := runPaperclip(verify); err == nil || !strings.Contains(err.Error(), "not pushed") {
		t.Fatalf("unpushed HEAD must fail verification, got %v", err)
	}
}

// commitAndPush commits paths, pushes HEAD to a fresh bare origin so a remote-tracking ref
// contains it, and records the new HEAD in f.head.
func (f *gateFixture) commitAndPush(t *testing.T, paths ...string) {
	t.Helper()
	remote := t.TempDir()
	if out, err := runFixtureGit(t, remote, f.env, "init", "-q", "--bare"); err != nil {
		t.Skipf("git init --bare failed in sandbox: %v (%s)", err, out)
	}
	for _, args := range [][]string{
		append([]string{"add", "--"}, paths...),
		{"commit", "-q", "-m", "harness"},
		{"remote", "add", "origin", remote},
		{"push", "-q", "-u", "origin", "HEAD"},
	} {
		if out, err := runFixtureGit(t, f.dir, f.env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	head, err := runFixtureGit(t, f.dir, f.env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v (%s)", err, head)
	}
	f.head = strings.TrimSpace(head)
}

// TestPaperclipVerify_InReviewReceiptOnDisk runs the documented in_review order with a receipt:
// commit and push, mint the receipt at the repository root the way `praetorctl gate run` does,
// attach that receipt to the disposition, then verify. The receipt file left untracked must not
// fail the clean-tree check.
func TestPaperclipVerify_InReviewReceiptOnDisk(t *testing.T) {
	f := newGateFixture(t)
	writePaperclipFixtureHarness(t, f.dir)
	f.pin(t, f.pub)
	f.commitAndPush(t, ".paperclip", ".standards.yaml")
	f.mintReceipt(t, f.priv, f.head)
	envelope, err := lockdown.LoadReceiptFile(filepath.Join(f.dir, gating.ReceiptFileName))
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	f.writeDisposition(t, "in_review", envelope)
	verify := []string{"verify", "--path=" + f.dir}

	// Positive: the receipt stays on disk, untracked, beside the disposition.
	if err := runPaperclip(verify); err != nil {
		t.Fatalf("in_review disposition with its untracked gate receipt rejected: %v", err)
	}

	// Negative: committing the receipt moves HEAD off the commit it attests.
	for _, args := range [][]string{
		{"add", gating.ReceiptFileName},
		{"commit", "-q", "-m", "receipt"},
		{"push", "-q"},
	} {
		if out, err := runFixtureGit(t, f.dir, f.env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := runPaperclip(verify); !errors.Is(err, lockdown.ErrCommitMismatch) {
		t.Fatalf("a committed receipt attests the parent, not HEAD; want ErrCommitMismatch, got %v", err)
	}
}
