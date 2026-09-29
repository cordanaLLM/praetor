// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// signReceipt signs output for rep's commit with the key lockdown.LoadSigningKey resolves, and
// returns the receipt only when `praetorctl gate verify` would accept its signer. When the
// manifest pins receipt.public_key (pinnedReceiptKey), the receipt must pass
// lockdown.VerifyPinnedReceipt against that key, the check gate verify applies; otherwise
// nothing is written and the error names the pinned key and the signing key. Before this check
// a workstation holding any other key minted a receipt the stage reported as passed and gate
// verify then rejected (#590).
//
// The pinned key is read before the signing key, so a malformed pin or an unreadable manifest
// fails the stage whether or not a signing key is configured, as it fails gate verify.
func signReceipt(ctx context.Context, repoDir string, rep *PipelineReport, output []byte) (*lockdown.ExecutionReceipt, error) {
	pinned, err := pinnedReceiptKey(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("Exit-0 receipt cannot be signed: %w", err)
	}
	priv, err := lockdown.LoadSigningKey()
	if err != nil {
		return nil, fmt.Errorf("Exit-0 receipt cannot be signed: %w", err)
	}
	receipt, err := lockdown.CreateReceipt(ReceiptCommand, 0, output, rep.CommitSHA, rep.Repository, priv)
	if err != nil {
		return nil, fmt.Errorf("create exit-0 receipt: %w", err)
	}
	if pinned == nil {
		return receipt, nil
	}
	if err := requirePinnedSigner(receipt, pinned, output); err != nil {
		return nil, err
	}
	return receipt, nil
}

// requirePinnedSigner verifies receipt against the pinned key with lockdown.VerifyPinnedReceipt,
// so the receipt stage refuses exactly what gate verify refuses. A receipt from another key is
// refused with lockdown.ErrKeyNotPinned, naming the signing key, the pinned key and both ways to
// reconcile them.
func requirePinnedSigner(receipt *lockdown.ExecutionReceipt, pinned ed25519.PublicKey, output []byte) error {
	err := lockdown.VerifyPinnedReceipt(receipt, pinned, output)
	if errors.Is(err, lockdown.ErrKeyNotPinned) {
		return fmt.Errorf("Exit-0 receipt not written, gate verify would reject it: %w: signing key %s, "+
			"pinned key %s in %s; sign with the pinned key's private half (%s, or the key file "+
			"'praetorctl gate keygen' wrote) or pin the signing key instead",
			lockdown.ErrKeyNotPinned, receipt.PublicKey, hex.EncodeToString(pinned), config.ManifestFileName,
			lockdown.SigningKeyEnv)
	}
	if err != nil {
		return fmt.Errorf("Exit-0 receipt not written, gate verify would reject it: %w", err)
	}
	return nil
}

// pinnedReceiptKey reads the receipt.public_key that repoDir's manifest pins through
// lockdown.PinnedPublicKey, the read `gate verify` resolves its default key with. A repository
// without a manifest, or whose manifest pins no key, gets a nil key and no error: nothing binds
// its receipts to a key, and the receipt stage signs as it always has. A manifest that cannot be
// read and a malformed key are errors, because gate verify fails on them too.
func pinnedReceiptKey(ctx context.Context, repoDir string) (ed25519.PublicKey, error) {
	pinned, err := lockdown.PinnedPublicKey(ctx, filepath.Join(repoDir, config.ManifestFileName))
	if errors.Is(err, lockdown.ErrNoPinnedKey) || errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve receipt.public_key: %w", err)
	}
	return pinned, nil
}
