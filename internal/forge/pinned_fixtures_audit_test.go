// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

var fixturesToday = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

const srtSample = "1\n00:00:01,000 --> 00:00:04,000\nHello world\n"

func initPinnedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testsupport.RunFixtureGit(t, dir, []string{"add", "."})
	return dir
}

func auditPinned(t *testing.T, root string, entries ...config.Exception) (string, error) {
	t.Helper()
	return AuditPinnedFixturesEOL(t.Context(), PinnedFixtureEOLOptions{
		Root:       root,
		Exceptions: entries,
		Today:      fixturesToday,
	})
}

// Negative planted red: testdata/sub.srt, a Go test pinning its sha256, no attribute ->
// one finding naming the file and the test.
func TestAuditPinnedFixturesEOL_Negative_PlantedRed(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
	})

	out, err := auditPinned(t, root)
	if err == nil {
		t.Fatalf("AuditPinnedFixturesEOL = %q, want failure for unpinned sub.srt", out)
	}
	if !strings.Contains(err.Error(), "testdata/sub.srt") || !strings.Contains(err.Error(), "sub_test.go") {
		t.Fatalf("AuditPinnedFixturesEOL err = %q, want naming testdata/sub.srt and sub_test.go", err.Error())
	}
	if !strings.Contains(err.Error(), "testdata/sub.srt -text") || !strings.Contains(err.Error(), "*.srt eol=lf") {
		t.Fatalf("AuditPinnedFixturesEOL err = %q, want fix line suggestions", err.Error())
	}
}

// A scan slower than the per-call budget leaves git check-attr its own budget (#1456): the
// planted red fixture is still reported as a finding, not as a deadline error.
func TestAuditPinnedFixturesEOL_Boundary_SlowScanKeepsTheCallBudget(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hex.EncodeToString(sum[:])),
	})
	savedTimeout, savedHook := pinnedFixturesTimeout, afterFixtureScan
	t.Cleanup(func() { pinnedFixturesTimeout, afterFixtureScan = savedTimeout, savedHook })
	pinnedFixturesTimeout = 2 * time.Second
	afterFixtureScan = func() { time.Sleep(3 * time.Second) }
	_, err := auditPinned(t, root)
	if err == nil || strings.Contains(err.Error(), "deadline") || !strings.Contains(err.Error(), "testdata/sub.srt") {
		t.Fatalf("a slow scan must leave check-attr its budget and report the finding, got %v", err)
	}
}

// Positive green: the same with *.srt -text in .gitattributes passes.
func TestAuditPinnedFixturesEOL_Positive_DashText(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
		".gitattributes":   "*.srt -text\n",
	})

	out, err := auditPinned(t, root)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditPinnedFixturesEOL = %q, %v; want pass with *.srt -text", out, err)
	}
}

// Positive green: the same with testdata/** eol=lf in .gitattributes passes.
func TestAuditPinnedFixturesEOL_Positive_EOL_LF(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
		".gitattributes":   "testdata/** eol=lf\n",
	})

	out, err := auditPinned(t, root)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditPinnedFixturesEOL = %q, %v; want pass with testdata/** eol=lf", out, err)
	}
}

// Boundary: a fixture whose hash appears nowhere is not reported.
func TestAuditPinnedFixturesEOL_Boundary_UnreferencedFixtureNotReported(t *testing.T) {
	root := initPinnedRepo(t, map[string]string{
		"testdata/unreferenced.srt": srtSample,
		"other_test.go":             "package main\n\nfunc TestOther(t *testing.T) {}\n",
	})

	out, err := auditPinned(t, root)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditPinnedFixturesEOL = %q, %v; want pass when fixture is not pinned", out, err)
	}
}

// Boundary: a binary-attributed file passes.
func TestAuditPinnedFixturesEOL_Boundary_BinaryAttributedPasses(t *testing.T) {
	sum := sha256.Sum256([]byte("binary data\n"))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst binSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/blob.bin": "binary data\n",
		"blob_test.go":      testBody,
		".gitattributes":    "*.bin binary\n",
	})

	out, err := auditPinned(t, root)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditPinnedFixturesEOL = %q, %v; want pass with binary attribute", out, err)
	}
}

// Boundary: an exception entry silences the finding.
func TestAuditPinnedFixturesEOL_Boundary_LiveExceptionSilences(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
	})

	ex := config.Exception{
		Rule:    config.ExceptionRulePlatformNeutrality,
		Path:    "testdata/sub.srt",
		Reason:  "legacy subtitle fixture pending migration",
		Expires: "2026-10-30",
	}

	out, err := auditPinned(t, root, ex)
	if err != nil || !strings.Contains(out, "excepted until 2026-10-30") {
		t.Fatalf("AuditPinnedFixturesEOL = %q, %v; want pass with live exception", out, err)
	}
}

// Boundary: an expired exception entry does not silence the finding.
func TestAuditPinnedFixturesEOL_Negative_ExpiredExceptionFails(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
	})

	ex := config.Exception{
		Rule:    config.ExceptionRulePlatformNeutrality,
		Path:    "testdata/sub.srt",
		Reason:  "legacy subtitle fixture pending migration",
		Expires: "2026-10-10",
	}

	_, err := auditPinned(t, root, ex)
	if err == nil || !strings.Contains(err.Error(), "expired 2026-10-10") || !strings.Contains(err.Error(), "testdata/sub.srt") {
		t.Fatalf("AuditPinnedFixturesEOL err = %v, want failure naming expired exception", err)
	}
}

// Boundary: a golden file named by path in a test is detected and passes when attributed.
func TestAuditPinnedFixturesEOL_Boundary_GoldenNamedByPath(t *testing.T) {
	testBody := "package main\n\nconst goldenFile = \"testdata/sample.golden\"\n"

	root := initPinnedRepo(t, map[string]string{
		"testdata/sample.golden": "golden output\n",
		"sample_test.go":         testBody,
	})

	// Without attribute -> fails
	_, err := auditPinned(t, root)
	if err == nil || !strings.Contains(err.Error(), "testdata/sample.golden") {
		t.Fatalf("AuditPinnedFixturesEOL want failure for unattributed sample.golden, got %v", err)
	}

	// With attribute -> passes
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.golden eol=lf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", ".gitattributes"})

	out, err := auditPinned(t, root)
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditPinnedFixturesEOL with *.golden eol=lf = %q, %v; want pass", out, err)
	}
}

// Negative: an entry for a fixture that already has an eol rule excuses nothing and fails.
func TestAuditPinnedFixturesEOL_Negative_StaleExceptionFails(t *testing.T) {
	sum := sha256.Sum256([]byte(srtSample))
	hash := hex.EncodeToString(sum[:])
	testBody := fmt.Sprintf("package main\n\nconst expectedSHA = %q\n", hash)

	root := initPinnedRepo(t, map[string]string{
		"testdata/sub.srt": srtSample,
		"sub_test.go":      testBody,
		".gitattributes":   "*.srt -text\n",
	})

	ex := config.Exception{
		Rule:    config.ExceptionRulePlatformNeutrality,
		Path:    "testdata/sub.srt",
		Reason:  "legacy subtitle fixture pending migration",
		Expires: "2026-10-30",
	}

	_, err := auditPinned(t, root, ex)
	if err == nil || !strings.Contains(err.Error(), "excuses nothing") {
		t.Fatalf("AuditPinnedFixturesEOL err = %v, want failure naming stale exception", err)
	}
}
