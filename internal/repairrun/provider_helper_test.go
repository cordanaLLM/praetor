//go:build unix

package repairrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProviderHelperRejectsChangedOrNonPrivateSource(t *testing.T) {
	for _, change := range []string{"changed", "public", "not executable", "symlink", "parent symlink", "fifo", "oversized"} {
		t.Run(change, func(t *testing.T) {
			cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
			switch change {
			case "changed":
				if err := os.WriteFile(cfg.TokenCommand, []byte("#!/bin/sh\nexit 19\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(cfg.TokenCommand, 0o755); err != nil {
					t.Fatal(err)
				}
			case "not executable":
				if err := os.Chmod(cfg.TokenCommand, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "helper-link")
				if err := os.Symlink(cfg.TokenCommand, link); err != nil {
					t.Fatal(err)
				}
				cfg.TokenCommand = link
			case "parent symlink":
				link := filepath.Join(t.TempDir(), "parent-link")
				if err := os.Symlink(filepath.Dir(cfg.TokenCommand), link); err != nil {
					t.Fatal(err)
				}
				cfg.TokenCommand = filepath.Join(link, filepath.Base(cfg.TokenCommand))
			case "fifo":
				fifo := filepath.Join(t.TempDir(), "fifo")
				if err := syscall.Mkfifo(fifo, 0o700); err != nil {
					t.Fatal(err)
				}
				cfg.TokenCommand = fifo
			case "oversized":
				if err := os.WriteFile(cfg.TokenCommand, []byte(strings.Repeat("x", providerPromptLimit+1)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if token, err := providerToken(ctx, cfg); err == nil || token != "" {
				t.Fatal("unsafe helper accepted")
			}
		})
	}
}

func TestProviderHelperOutputAndCancellation(t *testing.T) {
	scripts := []string{
		"exit 7", "printf ''", "printf 'wrong-token\\n'",
		"printf '%s\\nextra' '" + providerFixtureToken + "'",
		"printf '%s\\n' '" + providerFixtureToken + "' >&2; exit 9",
		"printf '%s\\n' '" + providerFixtureToken + "'; printf 'private diagnostic' >&2",
		"printf 'sk-'; head -c 4096 /dev/zero | tr '\\000' a",
		"sleep 30",
	}
	for index, script := range scripts {
		cfg := providerFixtureConfig(t, script)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		start := time.Now()
		token, err := providerToken(ctx, cfg)
		cancel()
		if token != "" || err == nil || strings.Contains(err.Error(), providerFixtureToken) || strings.Contains(err.Error(), "private diagnostic") {
			t.Errorf("helper case %d leaked or succeeded: %v", index, err)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("helper case %d exceeded cancellation bound", index)
		}
	}
}

func TestProviderHelperSnapshotAndInputCredentialRedaction(t *testing.T) {
	cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
	snapshot, err := providerHelperSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.TokenCommand, []byte("#!/bin/sh\nexit 11\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), providerFixtureToken) {
		t.Fatal("snapshot changed with original helper")
	}
	if _, err := providerHelperSnapshot(cfg); err == nil {
		t.Fatal("changed original helper accepted")
	}
	cfg = providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
	proposal, err := providerGenerate(t.Context(), cfg, "accidental credential "+providerFixtureToken, nil)
	if err == nil || proposal != nil || strings.Contains(err.Error(), providerFixtureToken) {
		t.Fatal("credential-containing context must be rejected before HTTP")
	}
}

func TestProviderTokenBoundary(t *testing.T) {
	for _, token := range []string{"sk-a", "sk-AbC_012.3", "sk-" + strings.Repeat("a", 4092)} {
		if !providerValidToken(token) {
			t.Error("valid token syntax rejected")
		}
	}
	for _, token := range []string{"sk-", "sk- a", "sk-a\r", "sk-a\n", "sk-a\"", "sk-" + strings.Repeat("a", 4093)} {
		if providerValidToken(token) {
			t.Error("invalid token syntax accepted")
		}
	}
}
