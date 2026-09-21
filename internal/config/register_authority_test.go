package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterAuthorityBindsCanonicalManifestBytes(t *testing.T) {
	root := t.TempDir()
	absent, err := LoadRegisterAuthority(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	emptySum := sha256.Sum256(nil)
	if absent.ManifestSHA256() != hex.EncodeToString(emptySum[:]) {
		t.Fatalf("absent manifest digest = %s", absent.ManifestSHA256())
	}
	resolution, err := absent.Resolve(SurfaceAgent, "ci_debugging")
	if err != nil || resolution.Register != TextRegisterInternal || resolution.Source != "surfaces.agent" ||
		resolution.ManifestSHA256 != absent.ManifestSHA256() {
		t.Fatalf("absent resolution = %+v, %v", resolution, err)
	}

	body := []byte("version: 1\nregister:\n  tasks:\n    ci_debugging: {register: social, max_tokens: 256}\n")
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	authority, err := LoadRegisterAuthority(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(body)
	task, err := authority.Resolve(SurfaceAgent, "ci_debugging")
	prompt, promptErr := authority.Resolve(SurfacePrompts, "ci_debugging")
	if err != nil || promptErr != nil || authority.ManifestSHA256() != hex.EncodeToString(wantSum[:]) ||
		task.Register != TextRegisterSocial || task.MaxTokens != 256 || task.Source != "tasks.ci_debugging" ||
		prompt.Register != TextRegisterInternal || prompt.Source != "surfaces.prompts" ||
		task.ManifestSHA256 != authority.ManifestSHA256() || prompt.ManifestSHA256 != authority.ManifestSHA256() {
		t.Fatalf("canonical authority: task=%+v prompt=%+v digest=%s errors=%v/%v", task, prompt, authority.ManifestSHA256(), err, promptErr)
	}
}

func TestRegisterAuthorityMaximumTaskBoundary(t *testing.T) {
	root := t.TempDir()
	label := strings.Repeat("x", 256)
	body := "version: 1\nregister:\n  tasks:\n    " + label + ": internal\n"
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	authority, err := LoadRegisterAuthority(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := authority.Resolve(SurfaceAgent, label)
	if err != nil || len(resolution.Source) != 262 || resolution.Source != "tasks."+label {
		t.Fatalf("maximum task resolution = %+v, %v", resolution, err)
	}
	if _, err := authority.Resolve(SurfaceAgent, label+"x"); err == nil {
		t.Fatal("overlong task label resolved")
	}
}

func TestRegisterAuthorityManifestCopyCannotMutateAuthority(t *testing.T) {
	body := []byte("version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n")
	authority, err := ParseRegisterAuthority(body, "fixture/.standards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	first, err := authority.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	first.Register.Tasks["ci_debugging"] = RegisterTask{Register: TextRegisterSocial}
	second, err := authority.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	resolution, resolveErr := authority.Resolve(SurfaceAgent, "ci_debugging")
	if resolveErr != nil || second.Register.Tasks["ci_debugging"].Register != TextRegisterDocs ||
		resolution.Register != TextRegisterDocs {
		t.Fatalf("returned manifest mutated authority: manifest=%+v resolution=%+v err=%v", second.Register, resolution, resolveErr)
	}
}

func TestRegisterAuthorityFailsClosed(t *testing.T) {
	if _, err := (RegisterAuthority{}).Resolve(SurfaceAgent, "ci_debugging"); err == nil {
		t.Fatal("zero authority resolved")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("register: [broken]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegisterAuthority(t.Context(), root); err == nil {
		t.Fatal("malformed manifest became authority")
	}
}
