// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: readRepoFile reads an ordinary manifest confined below the repository root, and
// manifestForLock decodes it into the manifest reconcileLockfile pins.
func TestReadRepoFileAndManifestForLock_Positive(t *testing.T) {
	s := lockAdoptSession(t)
	path := filepath.Join(s.repoPath, manifestFile)
	mustWrite(t, path, "version: 1\nprofiles: [framework]\n")

	data, err := readRepoFile(path)
	if err != nil || string(data) != "version: 1\nprofiles: [framework]\n" {
		t.Fatalf("readRepoFile = %q, %v", data, err)
	}

	manifest, err := manifestForLock(context.Background(), s)
	if err != nil || manifest.Version != 1 || len(manifest.Profiles) != 1 || manifest.Profiles[0] != "framework" {
		t.Fatalf("manifestForLock = %+v, %v", manifest, err)
	}
}

// Negative: a FIFO planted at the manifest path is refused within the deadline instead of
// blocking (BUG-822), and a manifest carrying an unknown top-level key is refused rather than
// silently accepted, since manifestForLock now decodes through config.DecodeManifest's
// KnownFields check.
func TestReadRepoFileAndManifestForLock_Negative(t *testing.T) {
	s := lockAdoptSession(t)
	fifo := filepath.Join(s.repoPath, manifestFile)
	testsupport.MakeFIFO(t, fifo)
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		_, readErr := readRepoFile(fifo)
		return readErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Fatalf("FIFO manifest = %v, want ErrNotRegularFile", err)
	}

	s2 := lockAdoptSession(t)
	mustWrite(t, filepath.Join(s2.repoPath, manifestFile), "version: 1\nbogus_field: true\n")
	if _, err := manifestForLock(context.Background(), s2); err == nil {
		t.Fatal("manifest with an unknown field was accepted")
	}
}

// Boundary: a manifest carrying a second YAML document is refused rather than decoded from
// its first document alone (BUG-857), and a manifest at exactly contextopt.MaxSourceBytes
// still reads while one byte past it is refused as oversized.
func TestReadRepoFileAndManifestForLock_Boundary(t *testing.T) {
	s := lockAdoptSession(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\n---\nversion: 1\n")
	if _, err := manifestForLock(context.Background(), s); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Fatalf("multi-document manifest = %v, want ErrYAMLNotSingleDocument", err)
	}

	s2 := lockAdoptSession(t)
	path := filepath.Join(s2.repoPath, manifestFile)
	body := "# " + strings.Repeat("x", int(contextopt.MaxSourceBytes)) + "\nversion: 1\n"
	mustWrite(t, path, body)
	if _, err := readRepoFile(path); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("oversize manifest = %v, want ErrFileTooLarge", err)
	}

	within := filepath.Join(s2.repoPath, "within.yaml")
	mustWrite(t, within, "version: 1\n")
	if _, err := config.DecodeManifest([]byte(mustRead(t, within))); err != nil {
		t.Fatalf("decoding a well-formed manifest below the bound failed: %v", err)
	}
}
