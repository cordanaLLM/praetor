// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// maxBinaryBytes bounds the digest read of one built binary (HISS-02: every I/O carries a
// scalar upper bound). No engine binary approaches this; it exists to fail loudly on a
// build gone wrong rather than to accommodate a real one.
const maxBinaryBytes = 512 << 20

// digestBytes returns the lowercase hex SHA-256 of data: the exact form
// config.InstallManifest validates and config.SelectOperatorSettings recomputes to detect
// drift, so both sides must use the identical algorithm.
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hashFile returns a built binary's digest and its own file mode.
func hashFile(path string) (digest string, mode os.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("workstation: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("workstation: %s is not a regular file", path)
	}
	if info.Size() > maxBinaryBytes {
		return "", 0, fmt.Errorf("workstation: %s exceeds %d bytes", path, maxBinaryBytes)
	}
	// #nosec G304 -- path is this package's own build output in a temp directory it created.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("workstation: read %s: %w", path, err)
	}
	return digestBytes(data), info.Mode().Perm(), nil
}

// settingsDocument turns a selected settings path and its bytes into the manifest's
// recorded form, or nil when the document is not configured.
func settingsDocument(doc config.SettingsDocument, data []byte) *config.InstalledDocument {
	if doc.Path == "" {
		return nil
	}
	return &config.InstalledDocument{Path: doc.Path, SHA256: digestBytes(data)}
}

// buildManifest assembles the install manifest for a completed install. previous is nil on
// a first install, or when no prior manifest could be read.
func buildManifest(now time.Time, engineCommit, binDir string, digests map[string]string,
	previous *config.InstalledPrevious, fleet, workstationDoc *config.InstalledDocument) config.InstallManifest {
	binaries := make(map[string]config.InstalledBinary, len(digests))
	for name, digest := range digests {
		binaries[name] = config.InstalledBinary{SHA256: digest}
	}
	return config.InstallManifest{
		Version:      config.InstallManifestVersion,
		EngineCommit: engineCommit,
		InstalledAt:  now.UTC().Format(time.RFC3339),
		BinDir:       binDir,
		Binaries:     binaries,
		Previous:     previous,
		Settings:     config.InstalledSettings{Fleet: fleet, Workstation: workstationDoc},
	}
}
