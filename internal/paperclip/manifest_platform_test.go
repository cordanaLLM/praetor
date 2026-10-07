package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// platformRepo returns an empty directory that is not a git repository, so no origin remote can
// name the platform and only the manifest decides it.
func platformRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dev", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePlatformManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: a complete repository section names the platform, even under a cancelled context,
// which governs the git lookup and the register skill read, not the manifest read. The synthesis
// as a whole then fails on the skill read, rather than writing a harness from a read it never
// made (harnessAbsentSkills).
func TestSynthesizeHarness_Positive_ManifestNamesPlatform(t *testing.T) {
	dir := platformRepo(t, "repo")
	writePlatformManifest(t, dir, "version: 1\nrepository:\n  owner: acme\n  name: widgets\n  forge: forgejo\nreceipt: {}\n")
	h, _, err := SynthesizeHarness(t.Context(), dir, unknownFacts)
	if err != nil || h.Platform != "acme/widgets" {
		t.Fatalf("SynthesizeHarness = %+v, %v; want platform acme/widgets", h, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if platform, err := resolvePlatform(ctx, dir); err != nil || platform != "acme/widgets" {
		t.Fatalf("resolvePlatform under a cancelled context = %q, %v; want acme/widgets", platform, err)
	}
	if h, _, err := SynthesizeHarness(ctx, dir, unknownFacts); !errors.Is(err, context.Canceled) || h != nil {
		t.Fatalf("SynthesizeHarness under a cancelled context = %+v, %v; want no harness and context.Canceled", h, err)
	}
}

// Negative: a multi-document manifest or a FIFO fails synthesis instead of silently falling
// back to the git identity (BUG-857, BUG-822).
func TestSynthesizeHarness_Negative_UnreadableManifestFails(t *testing.T) {
	dir := platformRepo(t, "multi")
	writePlatformManifest(t, dir, "repository:\n  owner: acme\n  name: first\n---\nrepository:\n  owner: acme\n  name: second\n")
	if _, _, err := SynthesizeHarness(context.Background(), dir, unknownFacts); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("multi-document manifest = %v, want ErrYAMLNotSingleDocument", err)
	}

	fifoDir := platformRepo(t, "fifo")
	testsupport.MakeFIFO(t, filepath.Join(fifoDir, manifestFile))
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		_, _, synthErr := SynthesizeHarness(context.Background(), fifoDir, unknownFacts)
		return synthErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO manifest = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an empty manifest names no platform, and without an origin remote synthesis fails
// with the fields to set; it never guesses an owner from the checkout path.
func TestSynthesizeHarness_Boundary_EmptyManifestNamesNoPlatform(t *testing.T) {
	dir := platformRepo(t, "empty")
	writePlatformManifest(t, dir, "")
	h, _, err := SynthesizeHarness(context.Background(), dir, unknownFacts)
	if err == nil {
		t.Fatalf("SynthesizeHarness = %+v; want an error naming repository.owner and repository.name", h)
	}
	if !strings.Contains(err.Error(), "repository.owner") || strings.Contains(err.Error(), "cordanaLLM") {
		t.Fatalf("SynthesizeHarness error = %v; want the missing fields named and no guessed owner", err)
	}
}
