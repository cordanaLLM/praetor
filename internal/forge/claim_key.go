// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// ClaimKeyDirName is the directory inside user configuration holding the claim key.
	ClaimKeyDirName = "praetor"
	// ClaimKeyFileName is the name of the on-disk claim tag key file.
	ClaimKeyFileName = "claim-tag.key"
	// ClaimKeySize is the size of the claim HMAC key in bytes.
	ClaimKeySize = 32
	// claimKeyPerm is the owner-only file permission for the key file.
	claimKeyPerm os.FileMode = 0o600
	// claimKeyDirPerm is the owner-only directory mode for the key file directory.
	claimKeyDirPerm os.FileMode = 0o700
)

// DefaultClaimKeyPath returns the default location of the claim key
// (<os.UserConfigDir()>/praetor/claim-tag.key).
func DefaultClaimKeyPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, ClaimKeyDirName, ClaimKeyFileName), nil
}

// LoadOrCreateDefaultClaimKey resolves the default key path and loads or creates the key.
func LoadOrCreateDefaultClaimKey() ([]byte, error) {
	return LoadOrCreateClaimKey("")
}

// LoadOrCreateClaimKey resolves path (or the default key path if path is empty),
// verifies permissions on an existing key, or creates a 32-byte key exclusively.
// Key material is never included in errors, logs, or error returns.
func LoadOrCreateClaimKey(path string) ([]byte, error) {
	resolved := path
	if resolved == "" {
		var err error
		resolved, err = DefaultClaimKeyPath()
		if err != nil {
			return nil, err
		}
	}
	raw, err := lockdown.ReadKeyFile(resolved)
	if err == nil {
		return parseClaimKey([]byte(raw))
	}
	if errors.Is(err, lockdown.ErrNoSigningKey) || errors.Is(err, os.ErrNotExist) {
		return createClaimKey(resolved)
	}
	return nil, err
}

func parseClaimKey(data []byte) ([]byte, error) {
	if len(data) == ClaimKeySize {
		key := make([]byte, ClaimKeySize)
		copy(key, data)
		return key, nil
	}
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == ClaimKeySize*2 {
		decoded, err := hex.DecodeString(trimmed)
		if err == nil && len(decoded) == ClaimKeySize {
			return decoded, nil
		}
	}
	return nil, errors.New("malformed claim tag key: expected 32 bytes")
}

func createClaimKey(path string) ([]byte, error) {
	dir := filepath.Dir(path)
	if err := util.MkdirSecure(dir, claimKeyDirPerm); err != nil {
		return nil, fmt.Errorf("create claim key directory: %w", err)
	}
	key := make([]byte, ClaimKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate claim key: %w", err)
	}
	err := util.WriteFileExclusive(path, key, claimKeyPerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			raw, readErr := lockdown.ReadKeyFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return parseClaimKey([]byte(raw))
		}
		return nil, fmt.Errorf("write claim key: %w", err)
	}
	return key, nil
}
