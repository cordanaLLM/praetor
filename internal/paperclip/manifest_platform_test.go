package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func writePlatformManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: a complete repository section names the platform, even under a cancelled context,
// which governs only the git lookup.
func TestSynthesizeHarness_Positive_ManifestNamesPlatform(t *testing.T) {
	dir := unresolvableRepo(t, "repo")
	writePlatformManifest(t, dir, "version: 1\nrepository:\n  owner: acme\n  name: widgets\nreceipt: {}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := SynthesizeHarness(ctx, dir)
	if err != nil || h.Platform != "acme/widgets" {
		t.Fatalf("SynthesizeHarness = %+v, %v; want platform acme/widgets", h, err)
	}
}

// Negative: a multi-document manifest or a FIFO fails synthesis instead of silently falling
// back to the git identity (BUG-857, BUG-822).
func TestSynthesizeHarness_Negative_UnreadableManifestFails(t *testing.T) {
	dir := unresolvableRepo(t, "multi")
	writePlatformManifest(t, dir, "repository:\n  owner: acme\n  name: first\n---\nrepository:\n  owner: acme\n  name: second\n")
	if _, err := SynthesizeHarness(context.Background(), dir); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("multi-document manifest = %v, want ErrYAMLNotSingleDocument", err)
	}

	fifoDir := unresolvableRepo(t, "fifo")
	testsupport.MakeFIFO(t, filepath.Join(fifoDir, manifestFile))
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		_, synthErr := SynthesizeHarness(context.Background(), fifoDir)
		return synthErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO manifest = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an empty manifest names no platform, so the basename fallback applies.
func TestSynthesizeHarness_Boundary_EmptyManifestFallsBack(t *testing.T) {
	dir := unresolvableRepo(t, "empty")
	writePlatformManifest(t, dir, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := SynthesizeHarness(ctx, dir)
	if err != nil || h.Platform != "cordanaLLM/empty" {
		t.Fatalf("SynthesizeHarness = %+v, %v; want the basename fallback", h, err)
	}
}
