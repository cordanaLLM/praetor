// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package figures

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// PlayerSourceFile is the repository-relative path of the file that embeds the player bundle
	// for release packaging.
	PlayerSourceFile = Directory + "/player_pack.go"
	// PlayerEmbedDirective is the exact go:embed line PlayerSourceFile declares.
	PlayerEmbedDirective = "//go:embed player/loader.js player/player.js player/THIRD-PARTY-LICENSES.txt"
	// PlayerArchiveName is the release asset archive filename.
	PlayerArchiveName = "figures-player.tar.gz"
	// PlayerLockName is the adopter lock filename written beside mkdocs_hook.py.
	PlayerLockName = "player.lock.json"
	// PlayerLockFile is the repository-relative path of the adopter lock file.
	PlayerLockFile = Directory + "/" + PlayerLockName
	// BundleScriptFile is the repository-relative path of the engine-only player bundler.
	BundleScriptFile = Directory + "/bundle.mjs"
	// MaxPlayerAssets bounds loops over the embedded player files (HISS-02).
	MaxPlayerAssets = 3
)

// playerEmbedNames are the three embedded player files in the exact tar entry order.
var playerEmbedNames = []string{
	"player/loader.js",
	"player/player.js",
	"player/THIRD-PARTY-LICENSES.txt",
}

//go:embed player/loader.js player/player.js player/THIRD-PARTY-LICENSES.txt
var playerAssets embed.FS

// PlayerNames returns the embedded player asset names in archive order.
func PlayerNames() []string {
	return slices.Clone(playerEmbedNames[:min(len(playerEmbedNames), MaxPlayerAssets)])
}

// PlayerAssetPaths returns the repository-relative paths of the embedded player files.
func PlayerAssetPaths() []string {
	paths := make([]string, 0, MaxPlayerAssets)
	for i := 0; i < len(playerEmbedNames) && i < MaxPlayerAssets; i++ {
		paths = append(paths, path.Join(Directory, playerEmbedNames[i]))
	}
	return paths
}

// ReadPlayer returns a copy of one embedded player file.
func ReadPlayer(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(playerAssets, "figures", playerEmbedNames, name)
}

// PlayerArchive builds the deterministic figures-player.tar.gz archive from the three embedded
// player files.
func PlayerArchive() ([]byte, error) {
	return playerArchiveFrom(playerAssets)
}

// PlayerArchiveSHA256 returns the lowercase hexadecimal SHA-256 digest of PlayerArchive.
func PlayerArchiveSHA256() (string, error) {
	return playerArchiveSHA256From(playerAssets)
}

func playerArchiveSHA256From(fsys fs.FS) (string, error) {
	data, err := playerArchiveFrom(fsys)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func playerArchiveFrom(fsys fs.FS) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Name = ""
	gw.ModTime = time.Time{}
	tw := tar.NewWriter(gw)
	for i := 0; i < len(playerEmbedNames) && i < MaxPlayerAssets; i++ {
		rel := playerEmbedNames[i]
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return nil, fmt.Errorf("read player asset %s: %w", rel, err)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     path.Base(rel),
			Size:     int64(len(data)),
			Mode:     0o644,
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
			ModTime:  time.Unix(0, 0).UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", rel, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("write tar body for %s: %w", rel, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close player tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close player gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderPlayerLock renders the canonical tools/figures/player.lock.json payload for the given
// engine release tag (empty for an unreleased build).
func RenderPlayerLock(release string) ([]byte, error) {
	sum, err := PlayerArchiveSHA256()
	if err != nil {
		return nil, fmt.Errorf("compute figure player archive digest: %w", err)
	}
	return fmt.Appendf(nil, "{\"asset\": %q, \"sha256\": %q, \"release\": %q}\n", PlayerArchiveName, sum, release), nil
}

var playerLockPattern = regexp.MustCompile(
	`^\{"asset": "figures-player\.tar\.gz", "sha256": "[0-9a-f]{64}", "release": "[^"\\]*"\}\n$`,
)

// IsPraetorPlayerLock reports whether data is an unedited Praetor player.lock.json in one
// consistent line-ending style, and whether it uses CRLF.
func IsPraetorPlayerLock(data []byte) (known, crlf bool) {
	normalized, isCRLF, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return false, false
	}
	return playerLockPattern.MatchString(normalized), isCRLF
}
