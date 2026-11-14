// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type pinScanMeasurement struct {
	listed, filtered, read, bytes int
	listing, reading, collecting  time.Duration
}

// TestScanRepositoryPins checks independently read repository pins without timing limits.
func TestScanRepositoryPins(t *testing.T) {
	requireGit(t)
	root := filepath.Join("..", "..")
	want := repositoryPinExpectations(t, root)
	pins, err := ScanRepositoryPins(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingRepositoryPins(pins, want); len(missing) != 0 {
		t.Fatalf("repository scan missed pins: %+v", missing)
	}
}

// TestScanRepositoryPinsContention optionally enforces a one-fifth deadline budget
// with two Go execution threads; the normal suite uses deterministic scan checks.
func TestScanRepositoryPinsContention(t *testing.T) {
	if os.Getenv("PRAETOR_PERF_TESTS") != "1" {
		t.Skip("performance timing requires PRAETOR_PERF_TESTS=1; host contention affects elapsed time")
	}
	requireGit(t)
	root := filepath.Join("..", "..")
	want := repositoryPinExpectations(t, root)
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	start := time.Now()
	pins, err := ScanRepositoryPins(t.Context(), root)
	elapsed := time.Since(start)
	t.Logf("repository pin scan: %s; budget: %s; GOMAXPROCS=2", elapsed, imageRefScanTimeout/5)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingRepositoryPins(pins, want); len(missing) != 0 {
		t.Fatalf("repository scan missed pins: %+v", missing)
	}
	if elapsed >= imageRefScanTimeout/5 {
		t.Fatalf("repository pin scan took %s, want less than %s (one fifth of imageRefScanTimeout)", elapsed, imageRefScanTimeout/5)
	}
}

// repositoryPinExpectations reads tagged pins directly, independently of scan listing.
func repositoryPinExpectations(tb testing.TB, root string) []ImagePin {
	tb.Helper()
	var want []ImagePin
	for _, rel := range []string{"Dockerfile", "docker/dev/Dockerfile"} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			tb.Fatal(err)
		}
		pins := CollectImagePins(rel, string(content))
		if len(pins) == 0 {
			tb.Fatalf("expected tagged image pins in %s", rel)
		}
		want = append(want, pins...)
	}
	return want
}

// missingRepositoryPins compares complete file, image and digest triples.
func missingRepositoryPins(pins, want []ImagePin) []ImagePin {
	var missing []ImagePin
	for _, pin := range want {
		if !slices.Contains(pins, pin) {
			missing = append(missing, pin)
		}
	}
	return missing
}

// TestMissingRepositoryPins rejects fabricated pins and mismatched triple fields.
func TestMissingRepositoryPins(t *testing.T) {
	want := repositoryPinExpectations(t, filepath.Join("..", ".."))
	wrongFile, wrongImage, wrongDigest := want[0], want[0], want[0]
	wrongFile.File += ".wrong"
	wrongImage.Image += "-wrong"
	wrongDigest.Digest = "wrong"
	for _, tc := range []struct {
		name        string
		pins        []ImagePin
		wantMissing int
	}{
		{name: "real pins", pins: want},
		{name: "empty", wantMissing: len(want)},
		{name: "two fabricated references", pins: []ImagePin{
			{File: "fake.go", Image: "fake:one", Digest: testPinnedDigest},
			{File: "fake.yml", Image: "fake:two", Digest: testPinnedDigest},
		}, wantMissing: len(want)},
		{name: "wrong file", pins: []ImagePin{wrongFile}, wantMissing: len(want)},
		{name: "wrong image", pins: []ImagePin{wrongImage}, wantMissing: len(want)},
		{name: "wrong digest", pins: []ImagePin{wrongDigest}, wantMissing: len(want)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if missing := missingRepositoryPins(tc.pins, want); len(missing) != tc.wantMissing {
				t.Fatalf("missing pins = %+v, want %d", missing, tc.wantMissing)
			}
		})
	}
}

// expectedPinMatchCalls counts marked contents among independently read scan candidates.
func expectedPinMatchCalls(t *testing.T, root string) map[string]int {
	t.Helper()
	files, err := RepositoryFiles(t.Context(), root, ScannedFile)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]int)
	err = iterateScannedFiles(t.Context(), root, files, ScannedFile, func(_ string, content []byte, readErr error) error {
		if readErr != nil {
			return readErr
		}
		if text := string(content); strings.Contains(text, "@sha256:") {
			want[text]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("repository has no marked files to exercise regexp calls")
	}
	return want
}

// TestScanRepositoryPinsSkipsMarkerFreeMatches counts one regexp call per marked
// read file, including duplicate contents, and zero calls for marker-free files.
func TestScanRepositoryPinsSkipsMarkerFreeMatches(t *testing.T) {
	requireGit(t)
	root := filepath.Join("..", "..")
	remaining := expectedPinMatchCalls(t, root)
	original := findTaggedDigests
	t.Cleanup(func() { findTaggedDigests = original })
	unexpected := 0
	findTaggedDigests = func(content string, limit int) [][]string {
		if remaining[content] == 0 {
			unexpected++
		} else {
			remaining[content]--
		}
		return original(content, limit)
	}
	if _, err := ScanRepositoryPins(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if unexpected != 0 {
		t.Errorf("regexp called %d times for marker-free or excess file contents", unexpected)
	}
	for _, count := range remaining {
		if count != 0 {
			t.Errorf("regexp missed %d marked files with identical contents", count)
		}
	}
}

// TestReadScannedFileSlowReader proves successful bytes cannot mask an expired read.
func TestReadScannedFileSlowReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	read := false
	content, err := readScannedFileUsing(ctx, ".", "Dockerfile", func(string, string, int64) ([]byte, error) {
		read = true
		<-ctx.Done()
		return []byte("FROM scratch"), nil
	})
	if !read || content != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow read = %q, %v; reader called=%v; want no bytes and DeadlineExceeded", content, err, read)
	}
	if !strings.Contains(err.Error(), "read Dockerfile context: context deadline exceeded") {
		t.Fatalf("deadline error lost the candidate name: %v", err)
	}
}

// TestCollectImagePinsMarkerBoundary retains pins after long marker-free prefixes
// and preserves an empty (non-nil) result for absent or invalid digest markers.
func TestCollectImagePinsMarkerBoundary(t *testing.T) {
	const pin = "golang:1.27-alpine@sha256:" + testPinnedDigest
	for _, tc := range []struct {
		name, content string
		want          int
	}{
		{name: "empty"},
		{name: "no marker", content: strings.Repeat("package example\n", 4096)},
		{name: "marker without image", content: "@sha256:" + testPinnedDigest},
		{name: "invalid digest", content: "golang:1.27-alpine@sha256:abc"},
		{name: "late marker", content: strings.Repeat("package example\n", 4096) + pin, want: 1},
		{name: "two pins", content: pin + "\n" + pin, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pins := CollectImagePins("fixture.go", tc.content)
			if pins == nil || len(pins) != tc.want {
				t.Fatalf("pins = %v (nil=%v), want %d pins and non-nil result", pins, pins == nil, tc.want)
			}
			for _, p := range pins {
				if p.File != "fixture.go" || p.Image != "golang:1.27-alpine" || p.Digest != testPinnedDigest {
					t.Fatalf("unexpected pin: %+v", p)
				}
			}
		})
	}
}

// measurePinScan separates listing, bounded reads and extraction without changing scan scope.
func measurePinScan(ctx context.Context, root string) (pinScanMeasurement, error) {
	var m pinScanMeasurement
	start := time.Now()
	files, err := RepositoryFiles(ctx, root, func(rel string) bool {
		m.listed++
		return ScannedFile(rel)
	})
	m.listing = time.Since(start)
	m.filtered = len(files)
	if err != nil {
		return m, err
	}
	start = time.Now()
	err = iterateScannedFiles(ctx, root, files, ScannedFile, func(rel string, content []byte, readErr error) error {
		if readErr != nil {
			return readErr
		}
		m.read++
		m.bytes += len(content)
		collectStart := time.Now()
		CollectImagePins(rel, string(content))
		m.collecting += time.Since(collectStart)
		return nil
	})
	m.reading = time.Since(start) - m.collecting
	return m, err
}

// BenchmarkScanRepositoryPins records the real scan plus the work and time of each stage.
func BenchmarkScanRepositoryPins(b *testing.B) {
	root := filepath.Join("..", "..")
	want := repositoryPinExpectations(b, root)
	m, err := measurePinScan(b.Context(), root)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("listed=%d filtered=%d read=%d bytes=%d listing=%s reading=%s collecting=%s",
		m.listed, m.filtered, m.read, m.bytes, m.listing, m.reading, m.collecting)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pins, scanErr := ScanRepositoryPins(b.Context(), root)
		if scanErr != nil {
			b.Fatal(scanErr)
		}
		if missing := missingRepositoryPins(pins, want); len(missing) != 0 {
			b.Fatalf("repository scan missed pins: %+v", missing)
		}
	}
}
