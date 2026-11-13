// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type pinScanMeasurement struct {
	listed, filtered, read, bytes int
	listing, reading, collecting  time.Duration
}

// TestScanRepositoryPinsContention reserves four fifths of the one-minute deadline
// for host contention: the complete scan must use less than one fifth with only
// two Go execution threads. Run with -race and CPU affinity restricted to two CPUs
// to reproduce the loaded pre-push budget without weakening the scan deadline.
func TestScanRepositoryPinsContention(t *testing.T) {
	requireGit(t)
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	start := time.Now()
	pins, err := ScanRepositoryPins(t.Context(), filepath.Join("..", ".."))
	elapsed := time.Since(start)
	t.Logf("repository pin scan: %s; budget: %s; GOMAXPROCS=2", elapsed, imageRefScanTimeout/5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinCopies(pins)) < 2 {
		t.Fatal("repository scan stopped finding pins")
	}
	if elapsed >= imageRefScanTimeout/5 {
		t.Fatalf("repository pin scan took %s, want less than %s (one fifth of imageRefScanTimeout)", elapsed, imageRefScanTimeout/5)
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
		if len(pinCopies(pins)) < 2 {
			b.Fatal("repository scan stopped finding pins")
		}
	}
}
