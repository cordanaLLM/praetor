package devcontainer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUnavailablePlaceholder writes what generation without a source root writes for
// profiles and returns its path and bytes.
func writeUnavailablePlaceholder(t *testing.T, profiles []string) (string, []byte) {
	t.Helper()
	bundle, err := PrepareBundle(t.Context(), "adopted/app", profiles, nil, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, bundle, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s changed: %v", path, err)
	}
}

func TestWriteBundleReplacesOwnUnavailablePlaceholderWithoutForce(t *testing.T) {
	for name, crlf := range map[string]bool{"lf": false, "crlf-checkout": true} {
		t.Run(name, func(t *testing.T) {
			path, placeholder := writeUnavailablePlaceholder(t, []string{"framework"})
			if crlf {
				placeholder = bytes.ReplaceAll(placeholder, []byte("\n"), []byte("\r\n"))
				if err := os.WriteFile(path, placeholder, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteBundle(t.Context(), path, preparedBootstrap(t), false); err != nil {
				t.Fatalf("the advised --source-root rerun stopped at Praetor's own placeholder: %v", err)
			}
			if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
				t.Fatalf("replaced bundle does not verify: %v", err)
			}
		})
	}
}

func TestWriteBundlePreservesAnythingButTheExactPlaceholder(t *testing.T) {
	for name, edit := range map[string]func([]byte) []byte{
		"edit-in-managed-format": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"remoteUser": "vscode"`), []byte(`"remoteUser": "root"`), 1)
		},
		"trailing-content": func(b []byte) []byte { return append(b, []byte("{}\n")...) },
		"bootstrap-field":  func(b []byte) []byte { return bytes.Replace(b, []byte("exit 1"), []byte("exit 0"), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			path, placeholder := writeUnavailablePlaceholder(t, []string{"framework"})
			edited := edit(placeholder)
			if bytes.Equal(edited, placeholder) {
				t.Fatal("fixture edit did not apply")
			}
			if err := os.WriteFile(path, edited, 0644); err != nil {
				t.Fatal(err)
			}
			err := WriteBundle(t.Context(), path, preparedBootstrap(t), false)
			if err == nil || !strings.Contains(err.Error(), "--force") {
				t.Fatalf("edited placeholder replaced without --force: %v", err)
			}
			assertFileBytes(t, path, edited)
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), bootstrapDockerfile)); !os.IsNotExist(err) {
				t.Fatalf("refused write left a companion behind: %v", err)
			}
		})
	}
}

func TestWriteBundleKeepsPlaceholderOfOtherProfilesBehindForce(t *testing.T) {
	path, placeholder := writeUnavailablePlaceholder(t, []string{"native-gpu-systems"})
	if err := WriteBundle(t.Context(), path, preparedBootstrap(t), false); err == nil {
		t.Fatal("placeholder rendered for other profiles replaced without --force")
	}
	assertFileBytes(t, path, placeholder)
	if err := WriteBundle(t.Context(), path, preparedBootstrap(t), true); err != nil {
		t.Fatalf("explicit --force refused: %v", err)
	}
}

func TestWriteBundleForceNeverDowngradesReadyBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, preparedBootstrap(t), false); err != nil {
		t.Fatal(err)
	}
	ready, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = WriteBundle(t.Context(), path, unavailable, true)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace the ready DevContainer bootstrap") {
		t.Fatalf("--force without a source root replaced a ready bootstrap: %v", err)
	}
	assertFileBytes(t, path, ready)
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatalf("refused downgrade disturbed the ready bundle: %v", err)
	}
	if err := WriteBundle(t.Context(), path, preparedBootstrap(t), true); err != nil {
		t.Fatalf("forced ready-to-ready regeneration refused: %v", err)
	}
}
