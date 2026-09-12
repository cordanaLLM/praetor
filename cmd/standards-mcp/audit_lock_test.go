package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const auditLockSource = "id: framework\nname: Framework\n"

func auditFixtureDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validAuditLock pins the fixture profile with a real source and aggregate hash.
func validAuditLock(t *testing.T) string {
	t.Helper()
	digest := auditFixtureDigest(auditLockSource)
	doc := map[string]any{
		"version": 1, "pinned_version": "v1.0.0",
		"digest":   auditFixtureDigest("profile:framework=" + digest + "\n"),
		"profiles": []map[string]string{{"id": "framework", "version": "v1.0.0", "digest": digest}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestServerAuditRejectsInvalidLockContents(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		content    func(*testing.T) string
	}{
		{"malformed JSON", "malformed JSON", func(*testing.T) string { return `{"version":1,}` }},
		{"unpinned", "exact SemVer", func(t *testing.T) string { return strings.ReplaceAll(validAuditLock(t), "v1.0.0", "latest") }},
		{"digest mismatch", "top-level digest", func(t *testing.T) string {
			return strings.Replace(validAuditLock(t), auditFixtureDigest("profile:framework="+auditFixtureDigest(auditLockSource)+"\n"), auditFixtureDigest("wrong"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, root := newFixtureServer(t)
			content := tc.content(t)
			writeFixtureFile(t, root, ".standards.lock", content)
			result := callTool(t, srv, "standards_audit", nil)
			expectError(t, tc.name, result, tc.want)
			if strings.Contains(result.Content[0].Text, "digests verified") || strings.Contains(result.Content[0].Text, "gates passed") {
				t.Fatal("invalid lock audit emitted a verification claim")
			}
			data, err := os.ReadFile(filepath.Join(root, ".standards.lock"))
			if err != nil || string(data) != content {
				t.Fatalf("audit changed lock bytes: %v", err)
			}
		})
	}
}

func TestAuditLockfileChecksLocalSourceDigest(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.lock", validAuditLock(t))
	writeFixtureFile(t, root, ".config/archetypes/framework.yaml", auditLockSource)
	manifest := &config.Manifest{Version: 1, Profiles: []string{"framework"}}
	if line, err := auditLockfile(context.Background(), root, manifest); err != nil || !strings.Contains(line, "digests verified") {
		t.Fatalf("valid source failed: %q %v", line, err)
	}
	writeFixtureFile(t, root, ".config/archetypes/framework.yaml", auditLockSource+"description: changed\n")
	if _, err := auditLockfile(context.Background(), root, manifest); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Fatalf("expected the shared digest mismatch error, got %v", err)
	}
}

func TestAuditLockfileRejectsOutsideSymlinkAndCancellation(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(outside, []byte(validAuditLock(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	manifest := &config.Manifest{Version: 1, Profiles: []string{"framework"}}
	if _, err := auditLockfile(context.Background(), root, manifest); err == nil {
		t.Fatal("outside lock symlink passed validation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := auditLockfile(ctx, root, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
