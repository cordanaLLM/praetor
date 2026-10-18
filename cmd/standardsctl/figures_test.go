// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

// Positive: figures pack writes the deterministic archive to --out and prints its sha256sum line.
func TestRunFiguresPack_Positive(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), figureassets.PlayerArchiveName)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("figures", []string{"pack", "--out", outPath})
	})
	if err != nil {
		t.Fatalf("figures pack failed: %v", err)
	}
	wantDigest, err := figureassets.PlayerArchiveSHA256()
	if err != nil {
		t.Fatalf("PlayerArchiveSHA256: %v", err)
	}
	wantLine := wantDigest + "  " + figureassets.PlayerArchiveName
	if strings.TrimSpace(out) != wantLine {
		t.Fatalf("stdout = %q, want %q", strings.TrimSpace(out), wantLine)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantDigest {
		t.Fatalf("file sha256 = %s, want %s", got, wantDigest)
	}
}

// Negative: missing subcommand, unknown subcommand, missing --out, or unwritable path errors.
func TestRunFiguresPack_Negative(t *testing.T) {
	if err := dispatchCommand("figures", nil); err == nil {
		t.Fatal("expected error when subcommand is omitted")
	}
	if err := dispatchCommand("figures", []string{"unknown"}); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if err := dispatchCommand("figures", []string{"pack"}); err == nil {
		t.Fatal("expected error when --out is omitted")
	}
	if err := dispatchCommand("figures", []string{"pack", "--out", "a.tar.gz", "extra"}); err == nil {
		t.Fatal("expected error for unexpected positional argument")
	}
	blocker := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(blocker, figureassets.PlayerArchiveName)
	if err := dispatchCommand("figures", []string{"pack", "--out", unwritable}); err == nil {
		t.Fatal("expected error when writing under a regular file")
	}
}

// Boundary: an existing file at --out is overwritten with the exact archive bytes.
func TestRunFiguresPack_BoundaryOverwritesExistingFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), figureassets.PlayerArchiveName)
	if err := os.WriteFile(outPath, []byte("stale archive bytes that must be replaced"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("figures", []string{"pack", "--out", outPath})
	}); err != nil {
		t.Fatalf("figures pack overwrite failed: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	want, err := figureassets.PlayerArchive()
	if err != nil {
		t.Fatalf("PlayerArchive: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("existing file was not overwritten with the canonical archive bytes")
	}
}
