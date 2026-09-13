package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityPackageScope(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSecurityPackages(root, root+"\n"+nested+"\n"+nested+"\n")
	if err != nil || strings.Join(got, ",") != ".,./nested" {
		t.Fatalf("scope: %v %v", got, err)
	}
	for _, raw := range []string{"", "\n", root + "\n\n" + nested, "./nested", t.TempDir(), filepath.Join(root, "missing"), strings.Repeat(root+"\n", maxSecurityPackages+1), strings.Repeat("x", 4<<20+1)} {
		if _, err := resolveSecurityPackages(root, raw); err == nil {
			t.Fatalf("invalid scope accepted: %d bytes", len(raw))
		}
	}
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSecurityPackages(root, link); err == nil {
		t.Fatal("escaping directory symlink accepted")
	}
	maximum := strings.Repeat(root+"\n", maxSecurityPackages)
	if _, err := resolveSecurityPackages(root, maximum); err != nil {
		t.Fatalf("bounded listing rejected: %v", err)
	}
}

func TestSecurityPackageListingFailurePreventsScanner(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled} {
		root := newGoModuleDir(t)
		writeFile(t, filepath.Join(root, GosecConfigFile), "{}\n")
		cfg, _ := newTestConfig(t, root, false)
		scanned := false
		cfg.run = func(_ context.Context, _, name string, _ ...string) (string, error) {
			if name == "go" {
				return "", failure
			}
			if name == "gosec" {
				scanned = true
			}
			return "", nil
		}
		_, err := runSecurityStage(context.Background(), cfg)
		if err == nil || scanned {
			t.Fatalf("empty/failed listing passed: %v scanned=%v", err, scanned)
		}
		if failure != nil && !errors.Is(err, failure) {
			t.Fatalf("listing error identity lost: %v", err)
		}
	}
}
