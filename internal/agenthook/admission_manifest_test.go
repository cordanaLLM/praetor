// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestAdmissionManifestForgeIndependent plants a wrong policy type on a custom host.
// The usable forge must survive with a notice and terminal PR heads must still be checked.
func TestAdmissionManifestForgeIndependent(t *testing.T) {
	repo := t.TempDir()
	initAdmissionRepo(t, repo, "feature-branch", "https://git.example.com/acme/widgets.git")
	policies := [...]string{"hooks: false\n", "exceptions: false\n", "overrides: false\n"}
	for i := 0; i < len(policies) && i < 3; i++ {
		policy := policies[i]
		t.Run(strings.TrimSpace(policy), func(t *testing.T) {
			assertTypedManifestPushHeads(t, repo, policy)
		})
	}
}

// assertTypedManifestPushHeads exercises both admission outcomes and the custom forge host.
func assertTypedManifestPushHeads(t *testing.T, repo, policy string) {
	t.Helper()
	writeAdmissionManifest(t, repo, "repository:\n  forge: forgejo\n"+policy)
	called := false
	pol := admissionPolicy(t).WithAdmissionPullRequests(repo, func(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		called = true
		if target.Forge != config.ForgeForgejo || declaredForgejoHost(ctx, repo) != "git.example.com" {
			t.Fatalf("lost custom forge declaration: %+v", target)
		}
		if target.Branch == "feature-branch" {
			return nil, nil
		}
		return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged}}, nil
	})
	v := pol.CommandIn(t.Context(), "PRAETOR_ADMIT=git-push git push origin feature-branch", repo)
	if !called || v.Outcome != Allow || !strings.Contains(v.Notice, config.ManifestFileName) {
		t.Fatalf("typed policy defect blocked feature push: called=%v verdict=%+v", called, v)
	}
	called = false
	v = pol.CommandIn(t.Context(), "PRAETOR_ADMIT=git-push git push origin merged-topic", repo)
	requireDeniedContaining(t, "typed policy defect + merged head", v, "#42")
	if !called || !strings.Contains(v.Notice, config.ManifestFileName) {
		t.Fatalf("typed policy defect bypassed head check: called=%v verdict=%+v", called, v)
	}
}

// writeAdmissionManifest writes a fixture under its temporary repository root.
func writeAdmissionManifest(t *testing.T, repo, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, config.ManifestFileName), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadPushManifestDeadline plants a stalled filesystem reader; admission must return
// on its deadline even when the filesystem has not yet returned from a syscall.
func TestReadPushManifestDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := readPushManifest(ctx, "manifest", func(context.Context, string) ([]byte, error) {
			close(entered)
			<-release
			return nil, nil
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("manifest reader never started")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stalled read error = %v, want DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manifest read waited for reader after deadline")
	}
	busyCtx, busyCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer busyCancel()
	if _, err := readPushManifest(busyCtx, "second", func(context.Context, string) ([]byte, error) {
		return nil, errors.New("second filesystem worker started while first still blocked")
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("busy reader must wait within deadline: %v", err)
	}
}

// TestPushManifestForgeFallback refuses unknown hosts with no unambiguous declaration,
// while retaining notices for defects and accepting a valid or empty document as before.
func TestPushManifestForgeFallback(t *testing.T) {
	cases := [...]struct {
		name, data string
		want       config.Forge
		notice     bool
	}{
		{"valid", "repository:\n  forge: forgejo\n", config.ForgeForgejo, false},
		{"unrelated repository type", "repository:\n  forge: forgejo\n  owner: []\n", config.ForgeForgejo, true},
		{"empty", "", "", false},
		{"malformed", "version: [\n", "", true},
		{"missing forge", "hooks: false\n", "", true},
		{"invalid forge", "repository:\n  forge: gitea\n", "", true},
		{"wrong forge type", "repository:\n  forge: []\n", "", true},
		{"duplicate forge", "repository:\n  forge: forgejo\n  forge: github\n", "", true},
		{"second document", "repository:\n  forge: forgejo\n---\nversion: 1\n", "", true},
		{"repository sequence", "repository: [forgejo]\n", "", true},
	}
	for i := 0; i < len(cases) && i < 10; i++ {
		tt := cases[i]
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			writeAdmissionManifest(t, repo, tt.data)
			got, notice, err := resolvePushForge(t.Context(), repo, util.GitRemote{Host: "git.example.com"})
			if got != tt.want || (notice != "") != tt.notice {
				t.Fatalf("forge=%q notice=%q error=%v", got, notice, err)
			}
			if tt.want == "" && !errors.Is(err, config.ErrForgeUndeclared) {
				t.Fatalf("unknown host without declaration: error=%v", err)
			}
			if tt.want != "" && err != nil {
				t.Fatalf("usable declaration refused: %v", err)
			}
		})
	}
}

// TestUnknownHostManifestNotice keeps the manifest error visible when no forge is inferable.
func TestUnknownHostManifestNotice(t *testing.T) {
	repo := t.TempDir()
	initAdmissionRepo(t, repo, "feature-branch", "https://git.example.com/acme/widgets.git")
	writeAdmissionManifest(t, repo, "version: [\n")
	pol := admissionPolicy(t).WithAdmissionPullRequests(repo, func(context.Context, PushTarget) ([]forge.PullRequestHead, error) {
		t.Error("unknown forge reached PR reader")
		return nil, nil
	})
	v := pol.CommandIn(t.Context(), "PRAETOR_ADMIT=git-push git push origin feature-branch", repo)
	if v.Outcome != Deny || !strings.Contains(v.Reason, config.ForgeKey) || !strings.Contains(v.Notice, config.ManifestFileName) {
		t.Fatalf("unparseable manifest on unknown host: %+v", v)
	}
}

// TestForgeManifestPushAdmissionVerdicts plants a broken manifest in a mixed push and
// forge-write command; both checks must retain their notices and foreign writes must deny.
func TestForgeManifestPushAdmissionVerdicts(t *testing.T) {
	repo := t.TempDir()
	initAdmissionRepo(t, repo, "feature-branch", "https://github.com/acme/widgets.git")
	writeAdmissionManifest(t, repo, "repository:\n  forge: github\nhooks: false\n")
	pol := admissionPolicy(t)
	cases := [...]struct {
		name, suffix string
		want         Outcome
		forgeFinding bool
	}{
		{"push only", "", Allow, false},
		{"repair write", ` && gh pr comment 1 --body "Repair ready"`, Allow, true},
		{"foreign write", ` && gh pr comment 1 --body "outside/project#2"`, Deny, true},
	}
	for i := 0; i < len(cases) && i < 3; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			command := "PRAETOR_ADMIT=git-push git push origin feature-branch" + tc.suffix
			v := evaluateCommand(t.Context(), Canonical{Command: command}, repo, Invocation{
				WorkDir: repo, Policy: pol, Getenv: noEnvironment,
			})
			if v.Outcome != tc.want || !strings.Contains(v.Notice, "git push target resolution:") {
				t.Fatalf("push verdict lost: %+v", v)
			}
			if got := strings.Contains(v.Notice+v.Reason, forgeTextRefRuleName+": manifest finding"); got != tc.forgeFinding {
				t.Fatalf("forge manifest finding=%v, want %v: %+v", got, tc.forgeFinding, v)
			}
			if tc.want == Deny && !strings.Contains(v.Reason, "outside/project#2") {
				t.Fatalf("foreign reference refusal lost: %+v", v)
			}
		})
	}
}

// TestPushManifestReadCancellation refuses cancelled work before reading or host fallback.
// A normal read receives an explicit deadline even when its parent has none.
func TestPushManifestReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readPushManifest(ctx, "manifest", func(context.Context, string) ([]byte, error) {
		t.Error("cancelled manifest read called filesystem")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error=%v", err)
	}
	if _, _, err := resolvePushForge(ctx, t.TempDir(), util.GitRemote{Host: "github.com"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read fell back to host: %v", err)
	}
	_, err := readPushManifest(t.Context(), "manifest", func(ctx context.Context, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > pushTargetTimeout {
			return nil, errors.New("manifest read missing bounded deadline")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDeclaredManifestReadConfinement refuses a symlink escaping the manifest's root.
func TestDeclaredManifestReadConfinement(t *testing.T) {
	directoryRepo := t.TempDir()
	if err := os.Mkdir(filepath.Join(directoryRepo, config.ManifestFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := declaredManifestForge(t.Context(), directoryRepo); got != "" || err == nil {
		t.Fatalf("directory manifest = %q, %v", got, err)
	}
	repo, outside := t.TempDir(), t.TempDir()
	writeAdmissionManifest(t, outside, "repository:\n  forge: forgejo\n")
	if err := os.Symlink(filepath.Join(outside, config.ManifestFileName), filepath.Join(repo, config.ManifestFileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := declaredManifestForge(t.Context(), repo); got != "" || err == nil {
		t.Fatalf("escaped manifest = %q, %v", got, err)
	}
}

// TestDeclaredManifestReadBoundary keeps the exact byte limit, absent-file behavior
// and refusal of oversized files when the context-aware reader is substituted.
func TestDeclaredManifestReadBoundary(t *testing.T) {
	repo := t.TempDir()
	if got, err := declaredManifestForge(t.Context(), repo); got != "" || err != nil {
		t.Fatalf("absent manifest = %q, %v", got, err)
	}
	manifest := "repository:\n  forge: forgejo\n#"
	manifest += strings.Repeat("x", contextopt.MaxSourceBytes-len(manifest))
	writeAdmissionManifest(t, repo, manifest)
	if got, err := declaredManifestForge(t.Context(), repo); got != config.ForgeForgejo || err != nil {
		t.Fatalf("exact limit = %q, %v", got, err)
	}
	writeAdmissionManifest(t, repo, manifest+"x")
	if _, err := declaredManifestForge(t.Context(), repo); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("over limit error = %v", err)
	}
}
