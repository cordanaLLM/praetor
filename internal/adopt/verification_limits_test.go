package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeVerificationLimitsDefaultsAndValidation(t *testing.T) {
	got, err := NormalizeVerificationLimits(nil)
	if err != nil || got.MaxEntries != maxVerificationEntries || got.MaxFileBytes != maxVerificationInputBytes {
		t.Fatalf("defaults changed: %+v, %v", got, err)
	}
	valid := VerificationLimits{MaxEntries: 6000, MaxFiles: 2, MaxDepth: 4, MaxFileBytes: 128 << 10, MaxTotalBytes: 256 << 10}
	if got, err := NormalizeVerificationLimits(&valid); err != nil || got != valid {
		t.Fatalf("valid explicit limits rejected: %+v, %v", got, err)
	}
	for name, bad := range map[string]VerificationLimits{
		"zero":             {},
		"entries ceiling":  {MaxEntries: 200001, MaxFiles: 1, MaxDepth: 1, MaxFileBytes: 1, MaxTotalBytes: 1},
		"file above total": {MaxEntries: 1, MaxFiles: 1, MaxDepth: 1, MaxFileBytes: 2, MaxTotalBytes: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeVerificationLimits(&bad); err == nil {
				t.Fatal("invalid limits accepted")
			}
		})
	}
}

func TestVerificationExplicitLimitsPermitLargeBoundedInputs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "meson.build"), []byte(strings.Repeat("x", 70<<10)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		if err := os.Mkdir(filepath.Join(root, "d", "x"+strconv.Itoa(i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadVerificationInputs(t.Context(), root); err == nil {
		t.Fatal("default bounds accepted oversized metadata")
	}
	limits := VerificationLimits{MaxEntries: 6000, MaxFiles: 4, MaxDepth: 8, MaxFileBytes: 128 << 10, MaxTotalBytes: 256 << 10}
	if _, err := ObserveVerificationPlanWithLimits(t.Context(), root, &limits); err != nil {
		t.Fatalf("explicit bounded limits rejected: %v", err)
	}
}

func TestVerificationLimitsCancellationAndAdoptValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ObserveVerificationPlanWithLimits(ctx, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan error=%v", err)
	}
	root := t.TempDir()
	if _, err := Adopt(t.Context(), AdoptOptions{Path: root, DryRun: true, VerificationLimits: &VerificationLimits{}}); err == nil {
		t.Fatal("invalid adoption limits accepted")
	}
}
