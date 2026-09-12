package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lockTestSource = "id: framework\nname: Framework\n"

func lockTestDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func lockTestDocument() map[string]any {
	digest := lockTestDigest(lockTestSource)
	return map[string]any{
		"version": 1, "pinned_version": "v1.0.0",
		"digest":   lockTestDigest("profile:framework=" + digest + "\n"),
		"profiles": []map[string]any{{"id": "framework", "version": "v1.0.0", "digest": digest}},
	}
}

func writeConfigLockFixture(t *testing.T, document map[string]any) (string, *Manifest) {
	t.Helper()
	root := t.TempDir()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".standards.lock"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, &Manifest{Version: 1, Profiles: []string{"framework"}}
}

func TestValidateLockfilePositiveLocalAndRemoteSources(t *testing.T) {
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	source := filepath.Join(root, ".config", "archetypes", "framework.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(lockTestSource), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{true, false} {
		if !local {
			if err := os.RemoveAll(filepath.Join(root, ".config")); err != nil {
				t.Fatal(err)
			}
		}
		result, err := ValidateLockfile(context.Background(), root, manifest)
		if err != nil || result == nil || result.Profiles != 1 || result.Facets != 0 {
			t.Fatalf("local=%v: result=%+v err=%v", local, result, err)
		}
	}
}

func TestValidateLockfileRelativeRoot(t *testing.T) {
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	source := filepath.Join(root, ".config", "archetypes", "framework.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(lockTestSource), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(root)
	t.Chdir(parent)
	if _, err := ValidateLockfile(context.Background(), filepath.Base(root), manifest); err != nil {
		t.Fatalf("relative repository root must remain supported: %v", err)
	}
}

func TestValidateLockfileNegativePinsAndDigests(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want error
	}{
		{"unpinned release", func(d map[string]any) { d["pinned_version"] = "latest" }, ErrLockVersionInvalid},
		{"entry range", func(d map[string]any) { lockTestProfile(t, d)["version"] = "^1.0.0" }, ErrLockVersionInvalid},
		{"schema version", func(d map[string]any) { d["version"] = 2 }, ErrLockVersionInvalid},
		{"missing entry", func(d map[string]any) { d["profiles"] = []map[string]any{} }, ErrLockEntryMissing},
		{"placeholder", func(d map[string]any) { lockTestProfile(t, d)["digest"] = "sha256:" + emptyInputDigest }, ErrLockDigestPlaceholder},
		{"aggregate mismatch", func(d map[string]any) { d["digest"] = lockTestDigest("wrong aggregate") }, ErrLockDigestMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := lockTestDocument()
			tc.edit(doc)
			root, manifest := writeConfigLockFixture(t, doc)
			if result, err := ValidateLockfile(context.Background(), root, manifest); !errors.Is(err, tc.want) || result != nil {
				t.Fatalf("expected %v with no verified result, got %+v / %v", tc.want, result, err)
			}
		})
	}
}

func TestValidateLockfileRejectsMalformedSources(t *testing.T) {
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	source := filepath.Join(root, ".config", "archetypes", "framework.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"id: [unterminated", "id: framework\nname: changed\n"} {
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateLockfile(context.Background(), root, manifest); err == nil {
			t.Fatal("local source parse/hash failure must not become a source-less pass")
		}
	}
}

func TestValidateLockfileBoundaryInputs(t *testing.T) {
	for _, body := range []string{"", `{"version":1,}`, `{"version":`, "version: 1\n---\nversion: 1\n"} {
		root, manifest := writeConfigLockFixture(t, lockTestDocument())
		if err := os.WriteFile(filepath.Join(root, ".standards.lock"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateLockfile(context.Background(), root, manifest); err == nil {
			t.Fatalf("invalid document %q passed", body)
		}
	}
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ValidateLockfile(ctx, root, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled audit: %v", err)
	}
	if _, err := ValidateLockfile(context.Background(), root, nil); err == nil {
		t.Fatal("nil manifest passed")
	}
}

func TestValidateLockfileEntryLimit(t *testing.T) {
	for _, count := range []int{maxLockEntries, maxLockEntries + 1} {
		doc := lockTestDocument()
		entries, lines := make([]map[string]any, count), make([]string, count)
		declared := make([]string, count)
		for i := range entries {
			id, digest := fmt.Sprintf("profile-%03d", i), lockTestDigest(fmt.Sprint(i))
			entries[i] = map[string]any{"id": id, "version": "1.2.3-rc.1+build", "digest": digest}
			declared[i], lines[i] = id, "profile:"+id+"="+digest
		}
		doc["profiles"], doc["digest"] = entries, lockTestDigest(strings.Join(lines, "\n")+"\n")
		root, manifest := writeConfigLockFixture(t, doc)
		manifest.Profiles = declared
		result, err := ValidateLockfile(context.Background(), root, manifest)
		if count == maxLockEntries && (err != nil || result.Profiles != count) {
			t.Fatalf("exact limit must preserve every entry: result=%+v err=%v", result, err)
		}
		if count > maxLockEntries && (err == nil || result != nil) {
			t.Fatalf("overflow must reject instead of truncate: result=%+v err=%v", result, err)
		}
	}
}

func TestNormalizeDigest_3D(t *testing.T) {
	// A plausible real digest: 64 hex characters with no repeating 16-character block.
	real := "0123456789abcdef" + "fedcba9876543210" + "00112233445566778899aabbccddeeff"

	// Positive: a well-formed digest is normalised to bare lowercase hex.
	got, err := normalizeDigest("sha256:" + strings.ToUpper(real))
	if err != nil {
		t.Fatalf("normalizeDigest: %v", err)
	}
	if got != real {
		t.Errorf("normalizeDigest = %q, want %q", got, real)
	}

	// Negative: wrong prefix, wrong length, non-hex.
	for _, bad := range []string{real, "md5:" + real, "sha256:" + real[:10], "sha256:" + strings.Repeat("z", 64)} {
		if _, err := normalizeDigest(bad); !errors.Is(err, ErrLockDigestMalformed) {
			t.Errorf("normalizeDigest(%q): expected ErrLockDigestMalformed, got %v", bad, err)
		}
	}

	// Boundary: placeholders of both known shapes.
	for _, placeholder := range []string{
		"sha256:" + emptyInputDigest,
		"sha256:" + strings.Repeat("1234567890abcdef", 4),
		"sha256:" + strings.Repeat("fedcba0987654321", 4),
	} {
		if _, err := normalizeDigest(placeholder); !errors.Is(err, ErrLockDigestPlaceholder) {
			t.Errorf("normalizeDigest(%q): expected ErrLockDigestPlaceholder, got %v", placeholder, err)
		}
	}

	// Boundary: whitespace around a valid digest is tolerated.
	if _, err := normalizeDigest("  sha256:" + real + "  "); err != nil {
		t.Errorf("expected surrounding whitespace to be tolerated, got %v", err)
	}
}

func lockTestProfile(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	profiles, ok := document["profiles"].([]map[string]any)
	if !ok || len(profiles) != 1 {
		t.Fatal("fixture must have exactly one profile")
	}
	return profiles[0]
}
