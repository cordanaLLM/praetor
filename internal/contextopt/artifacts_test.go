package contextopt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactsPrivateRoundTripAndNoOverwrite(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "pack")
	files := map[string][]byte{"test.json": []byte(`{"fixture":true}`)}
	if err := WriteArtifacts(ctx, dir, files); err != nil {
		t.Fatal(err)
	}
	raw, err := ReadSnapshot(ctx, filepath.Join(dir, "test.json"))
	if err != nil || string(raw) != string(files["test.json"]) {
		t.Fatalf("readback %s: %v", raw, err)
	}
	info, err := os.Stat(filepath.Join(dir, "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatal("artifact permissions")
	}
	if err := WriteArtifacts(ctx, dir, files); err == nil {
		t.Fatal("existing directory replaced")
	}
	if err := WriteArtifacts(ctx, filepath.Join(t.TempDir(), "bad"), map[string][]byte{"../escape": nil}); err == nil {
		t.Fatal("path escape accepted")
	}
}
