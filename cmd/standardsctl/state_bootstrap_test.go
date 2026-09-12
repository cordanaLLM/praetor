package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateInitIfAbsentCreatesOnlyNewLedger(t *testing.T) {
	dir := t.TempDir()
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err == nil {
		t.Fatal("direct audit accepted absent ledger")
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"init", "--if-absent", dir})
	}); err != nil {
		t.Fatalf("absent private ledger bootstrap failed: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err != nil {
		t.Fatalf("new ledger failed strict audit: %v", err)
	}
	missing := filepath.Join(dir, ".workingdir", "OPEN.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"init", "--if-absent", dir})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("bootstrap silently filled a partial ledger: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err == nil {
		t.Fatal("partial ledger passed audit after bootstrap")
	}
}
