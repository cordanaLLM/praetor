package schemacheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientschema"
)

const (
	onlineEnv  = "PRAETOR_CLIENT_SCHEMAS_ONLINE"
	refreshEnv = "PRAETOR_UPDATE_CLIENT_SCHEMAS"
)

var vendorDir = filepath.Join(repoRoot, "internal", "clientschema", "vendor")

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestVendoredSchemasEqualTheirUpstreamPins fetches every pinned URL and compares it with the
// vendored copy. It needs the network, so it runs only when asked and otherwise skips with that
// reason (HISS-21); the offline pin test in internal/clientschema always runs.
func TestVendoredSchemasEqualTheirUpstreamPins(t *testing.T) {
	if os.Getenv(onlineEnv) != "1" {
		t.Skipf("set %s=1 to fetch every pinned upstream URL; the vendored bytes are always checked against manifest.json by internal/clientschema", onlineEnv)
	}
	for _, source := range loadManifest(t).Sources {
		for _, file := range source.Files {
			t.Run(file.Path, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				data, err := Fetch(ctx, source.URL(file))
				SkipOffline(t, err)
				if got := digest(data); got != file.SHA256 {
					t.Fatalf("%s at %s has sha256 %s, the vendored copy is pinned at %s: refresh with %s=1", source.URL(file), source.Pin, got, file.SHA256, refreshEnv)
				}
			})
		}
	}
}

// TestRefreshVendor rewrites the vendor directory from the pins when asked.
func TestRefreshVendor(t *testing.T) {
	if os.Getenv(refreshEnv) != "1" {
		t.Skipf("set %s=1 to fetch the pinned schemas and rewrite internal/clientschema/vendor", refreshEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	err := Refresh(ctx, vendorDir, Fetch)
	SkipOffline(t, err)
}

func copyVendor(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	for _, rel := range append(vendoredPaths(t), clientschema.ManifestFile) {
		data, err := os.ReadFile(filepath.Join(vendorDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// pinnedFetcher serves the vendored bytes for every pinned URL, and for the file at changedPath
// (when not empty) the same bytes plus a newline, as an upstream that moved by one byte.
func pinnedFetcher(manifest *clientschema.Manifest, changedPath string) Fetcher {
	return func(_ context.Context, url string) ([]byte, error) {
		for _, source := range manifest.Sources {
			for _, file := range source.Files {
				if source.URL(file) != url {
					continue
				}
				data, err := clientschema.Read(file)
				if file.Path == changedPath {
					data = append(data, '\n')
				}
				return data, err
			}
		}
		return nil, errors.New("unexpected URL " + url)
	}
}

// TestRefreshRewritesFilesAndDigests drives Refresh through a fake fetcher: a changed upstream
// byte moves the file and its digest, and the manifest still parses.
func TestRefreshRewritesFilesAndDigests(t *testing.T) {
	dir := copyVendor(t)
	manifest := loadManifest(t)
	changed := manifest.Sources[0].Files[0]
	fetch := pinnedFetcher(manifest, changed.Path)
	if err := Refresh(context.Background(), dir, fetch); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, clientschema.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := clientschema.ParseManifest(raw)
	if err != nil {
		t.Fatalf("refreshed manifest is refused: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(changed.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if got := refreshed.Sources[0].Files[0].SHA256; got != digest(written) || got == changed.SHA256 {
		t.Fatalf("digest %s, file digest %s, old %s", got, digest(written), changed.SHA256)
	}
	if refreshed.Sources[1].Files[0].SHA256 != manifest.Sources[1].Files[0].SHA256 {
		t.Error("an untouched file's digest moved")
	}
}

// TestRefreshWritesNothingWhenOneFetchFails is the negative case: a failure leaves the vendor
// directory as it was.
func TestRefreshWritesNothingWhenOneFetchFails(t *testing.T) {
	dir := copyVendor(t)
	before, err := os.ReadFile(filepath.Join(dir, clientschema.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(_ context.Context, url string) ([]byte, error) {
		calls++
		if calls > 3 {
			return nil, errors.New("HTTP 500")
		}
		return []byte(`{"changed":true}`), nil
	}
	err = Refresh(context.Background(), dir, fetch)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("Refresh = %v, want the fetch error", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, clientschema.ManifestFile))
	if err != nil || string(after) != string(before) {
		t.Fatalf("manifest changed after a failed refresh: %v", err)
	}
	first := loadManifest(t).Sources[0].Files[0]
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(first.Path)))
	if err != nil || first.Verify(data) != nil {
		t.Fatal("a vendored file was rewritten by a failed refresh")
	}
}

// TestRefreshWithUnchangedUpstreamLeavesTheManifestByteIdentical keeps the manifest's layout
// stable, so a refresh produces a diff only where upstream moved.
func TestRefreshWithUnchangedUpstreamLeavesTheManifestByteIdentical(t *testing.T) {
	dir := copyVendor(t)
	manifest := loadManifest(t)
	fetch := pinnedFetcher(manifest, "")
	if err := Refresh(context.Background(), dir, fetch); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(vendorDir, clientschema.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, clientschema.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("an unchanged refresh rewrote the manifest")
	}
}
