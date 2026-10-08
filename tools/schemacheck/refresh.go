package schemacheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/clientschema"
)

// Fetcher reads one URL; Fetch is the production one and tests pass a fake.
type Fetcher func(ctx context.Context, url string) ([]byte, error)

// Refresh brings the vendor directory at dir (internal/clientschema/vendor) to the pins its
// manifest records: it fetches every file at its pinned URL, then rewrites the files and their
// sha256 values. Nothing is written unless every fetch succeeded, so a failure half way leaves
// the directory as it was. A bump flows as: Renovate moves a pin in manifest.json, the takeover
// runs this (PRAETOR_UPDATE_CLIENT_SCHEMAS=1 go test ./tools/schemacheck -run TestRefreshVendor),
// then go generate ./internal/clientschema regenerates the types, and the failing tests of the
// branch name every field the new schema no longer accepts.
func Refresh(ctx context.Context, dir string, fetch Fetcher) error {
	manifestPath := filepath.Join(dir, clientschema.ManifestFile)
	// #nosec G304 -- dir is the vendor directory the caller names; the manifest name is fixed.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := clientschema.ParseManifest(raw)
	if err != nil {
		return err
	}
	fetched := map[string][]byte{}
	for i := range manifest.Sources {
		source := &manifest.Sources[i]
		for j := range source.Files {
			file := &source.Files[j]
			data, err := fetch(ctx, source.URL(*file))
			if err != nil {
				return fmt.Errorf("%s: %w", file.Path, err)
			}
			sum := sha256.Sum256(data)
			file.SHA256 = hex.EncodeToString(sum[:])
			fetched[file.Path] = data
		}
	}
	for path, data := range fetched {
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
