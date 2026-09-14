package harvester

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
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestOnboardRepositoryCancellationDuringIdentity(t *testing.T) {
	bin, repo := t.TempDir(), t.TempDir()
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := OnboardRepository(ctx, repo, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline error: %v", err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("onboarding wrote after cancellation: %v", entries)
	}
}

func TestOnboardRepositoryConfinesGeneratedOutputs(t *testing.T) {
	for _, rel := range []string{"CLAUDE.md", ".vscode"} {
		t.Run(rel, func(t *testing.T) {
			repo, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "protected")
			if rel == ".vscode" {
				target = outside
			} else {
				mustWriteFile(t, target, "keep")
			}
			if err := os.Symlink(target, filepath.Join(repo, rel)); err != nil {
				t.Fatal(err)
			}
			_, err := OnboardRepository(context.Background(), repo, false)
			if err == nil {
				t.Fatal("outside output symlink accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if rel == ".vscode" && len(entries) != 0 {
				t.Fatalf("wrote outside repo: %v", entries)
			}
			if rel == "CLAUDE.md" {
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "keep" {
					t.Fatalf("outside content changed: %q %v", data, err)
				}
			}
		})
	}
}

// verifiedOnboardFixture supplies a source-less consumer lock whose pins are declared
// by the caller. Production onboarding must preserve and validate those caller pins.
func verifiedOnboardFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, ".standards.yaml"), "version: 1\nprofiles: [framework]\n")
	digest := sha256.Sum256([]byte("caller-pinned fixture archetype"))
	pin := "sha256:" + hex.EncodeToString(digest[:])
	aggregate := sha256.Sum256([]byte("profile:framework=" + pin + "\n"))
	lock := map[string]any{"version": 1, "pinned_version": "v1.2.3", "digest": "sha256:" + hex.EncodeToString(aggregate[:]),
		"profiles": []map[string]string{{"id": "framework", "version": "v1.2.3", "digest": pin}}}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(repo, ".standards.lock"), string(data))
	return repo
}

func TestOnboardRepositoryVerifiedCallerLock(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	before, err := os.ReadFile(filepath.Join(repo, ".standards.lock"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil || !plan.LockVerified {
		t.Fatalf("valid caller lock rejected: %+v %v", plan, err)
	}
	after, err := os.ReadFile(filepath.Join(repo, ".standards.lock"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("caller pins changed: %v", err)
	}
}

func TestWriteOnboardEditorsMergesExistingHumanConfiguration(t *testing.T) {
	repo := t.TempDir()
	settingsPath := filepath.Join(repo, ".vscode", "settings.json")
	extensionsPath := filepath.Join(repo, ".vscode", "extensions.json")
	editorConfigPath := filepath.Join(repo, ".editorconfig")
	mustWriteFile(t, settingsPath, `{"editor.fontSize":42}`)
	mustWriteFile(t, extensionsPath, `{"recommendations":["human.extension"]}`)
	mustWriteFile(t, editorConfigPath, "root = true\n# human policy\n")

	if err := writeOnboardEditors(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(settings), `"editor.fontSize": 42`) || !strings.Contains(string(settings), `"files.insertFinalNewline": true`) {
		t.Fatalf("existing VS Code settings were not safely merged: %s %v", settings, err)
	}
	extensions, err := os.ReadFile(extensionsPath)
	if err != nil || !strings.Contains(string(extensions), "human.extension") {
		t.Fatalf("existing extension recommendation was lost: %s %v", extensions, err)
	}
	if editorConfig, err := os.ReadFile(editorConfigPath); err != nil || string(editorConfig) != "root = true\n# human policy\n" {
		t.Fatalf("human non-JSON editor config changed: %s %v", editorConfig, err)
	}
}

func TestWriteOnboardEditorsPropagatesCancellation(t *testing.T) {
	repo := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeOnboardEditors(ctx, repo); !errors.Is(err, context.Canceled) {
		t.Fatalf("editor onboarding lost cancellation: %v", err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled editor onboarding mutated workspace: %v %v", entries, err)
	}
}

func TestOnboardRepositoryReportsInvalidExistingLock(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	path := filepath.Join(repo, ".standards.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(data), "v1.2.3", "latest", 1)
	mustWriteFile(t, path, invalid)
	plan, err := OnboardRepository(context.Background(), repo, false)
	if !errors.Is(err, ErrOnboardingIncomplete) || !errors.Is(err, config.ErrLockVersionInvalid) {
		t.Fatalf("lost lock failure: %v", err)
	}
	if plan == nil || plan.LockVerified {
		t.Fatalf("missing truthful partial plan: %+v", plan)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != invalid {
		t.Fatalf("invalid caller lock was replaced: %v", err)
	}
}
