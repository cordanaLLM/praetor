package lockdown

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func writeKeyManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Positive: the pinned key is read from the receipt section of a manifest that carries other
// sections, which this reader does not own.
func TestPinnedPublicKey_Positive_SectionAmongUnrelatedKeys(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	path := writeKeyManifest(t, "version: 1\nrepository:\n  owner: acme\n  name: demo\nreceipt:\n  public_key: \""+hex.EncodeToString(pub)+"\"\n")
	loaded, err := PinnedPublicKey(context.Background(), path)
	if err != nil || !loaded.Equal(pub) {
		t.Fatalf("PinnedPublicKey = %x, %v; want the pinned key", loaded, err)
	}
}

// Negative: a key in either document of a multi-document manifest is refused, so the trust
// anchor never comes from a document the policy loader refuses (BUG-857); a FIFO is refused
// within the deadline instead of blocking (BUG-822).
func TestPinnedPublicKey_Negative_MultiDocumentAndFIFO(t *testing.T) {
	first, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	path := writeKeyManifest(t, "receipt:\n  public_key: \""+hex.EncodeToString(first)+"\"\n---\nreceipt:\n  public_key: \""+hex.EncodeToString(second)+"\"\n")
	if _, err := PinnedPublicKey(context.Background(), path); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("multi-document manifest = %v, want ErrYAMLNotSingleDocument", err)
	}

	fifo := filepath.Join(t.TempDir(), ".standards.yaml")
	testsupport.MakeFIFO(t, fifo)
	err = testsupport.RunWithin(t, 10*time.Second, func() error {
		_, readErr := PinnedPublicKey(context.Background(), fifo)
		return readErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO manifest = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an empty manifest pins no key, and one byte past the manifest bound is refused
// rather than parsed.
func TestPinnedPublicKey_Boundary_EmptyAndOversize(t *testing.T) {
	if _, err := PinnedPublicKey(context.Background(), writeKeyManifest(t, "")); !errors.Is(err, ErrNoPinnedKey) {
		t.Errorf("empty manifest = %v, want ErrNoPinnedKey", err)
	}
	oversize := "# " + strings.Repeat("x", 1<<20) + "\n"
	if _, err := PinnedPublicKey(context.Background(), writeKeyManifest(t, oversize)); !errors.Is(err, util.ErrFileTooLarge) {
		t.Errorf("oversize manifest = %v, want ErrFileTooLarge", err)
	}
}
