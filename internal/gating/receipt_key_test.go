// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// pinnedReceiptConfig returns a receipt-stage configuration over a fresh directory whose
// manifest holds manifest (no manifest at all when it is empty). The tree is clean and Go was
// verified, so the key is the only thing the receipt stage can refuse on.
func pinnedReceiptConfig(t *testing.T, manifest string) *stageConfig {
	t.Helper()
	repoDir := t.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(repoDir, config.ManifestFileName), []byte(manifest), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	cfg, _ := newTestConfig(t, repoDir, false)
	cfg.rep.Repository = "acme/widget"
	cfg.rep.CommitSHA = "0123456789abcdef0123456789abcdef01234567"
	cfg.rep.WorktreeClean = true
	cfg.verified = []string{languageGo}
	return cfg
}

// pinManifest renders a manifest that pins pub as receipt.public_key.
func pinManifest(pub ed25519.PublicKey) string {
	return "version: 1\nreceipt:\n  public_key: \"" + hex.EncodeToString(pub) + "\"\n"
}

// requireNoReceipt fails the test when the receipt stage wrote a receipt or recorded one.
func requireNoReceipt(t *testing.T, cfg *stageConfig) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(cfg.repoDir, ReceiptFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused run wrote a receipt: %v", err)
	}
	if cfg.rep.ReceiptSignature != "" || cfg.rep.ReceiptPath != "" {
		t.Errorf("a refused run recorded receipt metadata: %+v", cfg.rep)
	}
}

// requireReceiptSignedBy loads the written receipt and verifies it against key the way
// `gate verify` does (lockdown.VerifyPinnedReceiptFile).
func requireReceiptSignedBy(t *testing.T, cfg *stageConfig, key ed25519.PublicKey) {
	t.Helper()
	rf, err := lockdown.LoadReceiptFile(filepath.Join(cfg.repoDir, ReceiptFileName))
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	if err := lockdown.VerifyPinnedReceiptFile(rf, key); err != nil {
		t.Fatalf("written receipt does not verify against %s: %v", hex.EncodeToString(key), err)
	}
}

// Positive: a signing key that is the pinned receipt.public_key mints a receipt that verifies
// against the key `gate verify` reads from the manifest (lockdown.PinnedPublicKey).
func TestRunReceiptStage_Positive_PinnedSigningKeySigns(t *testing.T) {
	signing := sandboxReceiptKey(t)
	cfg := pinnedReceiptConfig(t, pinManifest(signing))

	if _, err := runReceiptStage(t.Context(), cfg); err != nil {
		t.Fatalf("receipt stage refused the pinned signing key: %v", err)
	}
	pinned, err := lockdown.PinnedPublicKey(t.Context(), filepath.Join(cfg.repoDir, config.ManifestFileName))
	if err != nil {
		t.Fatalf("PinnedPublicKey: %v", err)
	}
	requireReceiptSignedBy(t, cfg, pinned)
}

// Negative: a signing key other than the pinned receipt.public_key writes no receipt, and the
// refusal names both keys. Before #590 the stage passed and gate verify rejected the receipt.
func TestRunReceiptStage_Negative_UnpinnedSigningKeyRefused(t *testing.T) {
	signing := sandboxReceiptKey(t)
	pinned, _, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	cfg := pinnedReceiptConfig(t, pinManifest(pinned))

	_, err = runReceiptStage(t.Context(), cfg)
	if !errors.Is(err, lockdown.ErrKeyNotPinned) {
		t.Fatalf("want ErrKeyNotPinned, got %v", err)
	}
	for label, key := range map[string]ed25519.PublicKey{"pinned": pinned, "signing": signing} {
		if !strings.Contains(err.Error(), hex.EncodeToString(key)) {
			t.Errorf("refusal does not name the %s key %x: %v", label, key, err)
		}
	}
	requireNoReceipt(t, cfg)
}

// Boundary: a repository that pins no key signs with whatever key is configured, as before
// #590; a malformed pin and an unreadable manifest fail the stage as they fail gate verify.
func TestRunReceiptStage_Boundary_PinnedKeyResolution(t *testing.T) {
	unpinned := []struct{ name, manifest string }{
		{"no manifest", ""},
		{"no receipt section", "version: 1\n"},
		{"empty public_key", "version: 1\nreceipt:\n  public_key: \"\"\n"},
	}
	for _, tc := range unpinned {
		t.Run(tc.name, func(t *testing.T) {
			signing := sandboxReceiptKey(t)
			cfg := pinnedReceiptConfig(t, tc.manifest)
			if _, err := runReceiptStage(t.Context(), cfg); err != nil {
				t.Fatalf("an unpinned repository was refused a receipt: %v", err)
			}
			requireReceiptSignedBy(t, cfg, signing)
		})
	}

	refused := []struct {
		name, manifest string
		want           error
	}{
		{"malformed public_key", "version: 1\nreceipt:\n  public_key: \"abc\"\n", lockdown.ErrMalformedPinnedKey},
		// One hex character short of an Ed25519 public key.
		{"short public_key", "version: 1\nreceipt:\n  public_key: \"" + strings.Repeat("a", 2*ed25519.PublicKeySize-2) + "\"\n",
			lockdown.ErrMalformedPinnedKey},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			sandboxReceiptKey(t)
			cfg := pinnedReceiptConfig(t, tc.manifest)
			if _, err := runReceiptStage(t.Context(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			requireNoReceipt(t, cfg)
		})
	}

	// The pin is read before the signing key, so a malformed pin is what a keyless run reports.
	t.Run("malformed public_key without a signing key", func(t *testing.T) {
		sandboxReceiptKey(t)
		t.Setenv(lockdown.SigningKeyEnv, "")
		cfg := pinnedReceiptConfig(t, "version: 1\nreceipt:\n  public_key: \"abc\"\n")
		_, err := runReceiptStage(t.Context(), cfg)
		if !errors.Is(err, lockdown.ErrMalformedPinnedKey) || errors.Is(err, lockdown.ErrNoSigningKey) {
			t.Fatalf("want ErrMalformedPinnedKey ahead of the missing signing key, got %v", err)
		}
		requireNoReceipt(t, cfg)
	})

	t.Run("second manifest document", func(t *testing.T) {
		signing := sandboxReceiptKey(t)
		cfg := pinnedReceiptConfig(t, "version: 1\n---\n"+pinManifest(signing))
		_, err := runReceiptStage(t.Context(), cfg)
		if err == nil || !strings.Contains(err.Error(), "receipt.public_key") {
			t.Fatalf("want a refusal naming receipt.public_key, got %v", err)
		}
		requireNoReceipt(t, cfg)
	})
}
