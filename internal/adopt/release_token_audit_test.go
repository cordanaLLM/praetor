package adopt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

var releaseTokenToday = time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)

const passingReleaseWorkflow = `
jobs:
  release:
    runs-on: ubuntu-26.04
    steps:
      - name: Mint the release token
        id: app-token
        uses: actions/create-github-app-token@64ab729a6294000f5aa4a4bc0b73d1f32bc45510  # v3.3.0
        with:
          client-id: ${{ vars.RELEASE_APP_CLIENT_ID }}
          private-key: ${{ secrets.RELEASE_APP_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`

const failingReleaseWorkflow = `
jobs:
  release:
    runs-on: ubuntu-26.04
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ github.token }}
`

func releaseTokenEntry(path, entered, expires string, category string) config.Exception {
	return config.Exception{
		Rule:     config.ExceptionRuleReleaseToken,
		Path:     path,
		Category: category,
		Entered:  entered,
		Expires:  expires,
		Reason:   "app token migration in progress",
	}
}

// TestAuditReleaseToken_Passing verifies that a conformant release-please workflow passes.
func TestAuditReleaseToken_Passing(t *testing.T) {
	root := supplyChainRepo(t, map[string]string{
		".github/workflows/release.yml": passingReleaseWorkflow,
	})
	out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
		Root:  root,
		Today: releaseTokenToday,
	})
	if err != nil {
		t.Fatalf("AuditReleaseToken = %v; want pass", err)
	}
	want := "[PASS] Release token: 1 release-please step takes token from a GitHub App token step."
	if !strings.Contains(out, want) {
		t.Errorf("output = %q; want %q", out, want)
	}
}

// TestAuditReleaseToken_LiveEntryPasses verifies that a live exception excuses the failing workflow.
func TestAuditReleaseToken_LiveEntryPasses(t *testing.T) {
	root := supplyChainRepo(t, map[string]string{
		".github/workflows/release.yml": failingReleaseWorkflow,
	})
	entry := releaseTokenEntry(".github/workflows/release.yml", "2026-10-12", "2026-10-22", "C")
	out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
		Root:       root,
		Exceptions: []config.Exception{entry},
		Today:      releaseTokenToday,
	})
	if err != nil {
		t.Fatalf("AuditReleaseToken failed: %v", err)
	}
	if !strings.Contains(out, "excepted until 2026-10-22 by the exceptions entry") {
		t.Errorf("output = %q; want excepted notice", out)
	}
	if !strings.Contains(out, "app token migration in progress") {
		t.Errorf("output = %q; want exception reason", out)
	}
}

// TestAuditReleaseToken_ExpiredEntryFails verifies that an expired exception causes a failure.
func TestAuditReleaseToken_ExpiredEntryFails(t *testing.T) {
	root := supplyChainRepo(t, map[string]string{
		".github/workflows/release.yml": failingReleaseWorkflow,
	})
	entry := releaseTokenEntry(".github/workflows/release.yml", "2026-10-01", "2026-10-11", "C")
	_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
		Root:       root,
		Exceptions: []config.Exception{entry},
		Today:      releaseTokenToday,
	})
	if err == nil {
		t.Fatal("AuditReleaseToken passed; want failure for expired entry")
	}
	want := "expired on 2026-10-11, so the release-please step above in .github/workflows/release.yml fails as if it were not declared"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want %q", err.Error(), want)
	}
}

// TestAuditReleaseToken_StaleEntryFails verifies that an unneeded exception entry fails.
func TestAuditReleaseToken_StaleEntryFails(t *testing.T) {
	t.Run("entry naming another workflow without findings", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			".github/workflows/release.yml": failingReleaseWorkflow,
		})
		entry := releaseTokenEntry(".github/workflows/ci.yml", "2026-10-12", "2026-10-22", "C")
		_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
			Root:       root,
			Exceptions: []config.Exception{entry},
			Today:      releaseTokenToday,
		})
		if err == nil {
			t.Fatal("AuditReleaseToken passed; want failure for stale entry")
		}
		if !strings.Contains(err.Error(), "excuse no step") {
			t.Errorf("error = %q; want excuse no step", err.Error())
		}
	})

	t.Run("entry when workflow passes cleanly", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			".github/workflows/release.yml": passingReleaseWorkflow,
		})
		entry := releaseTokenEntry(".github/workflows/release.yml", "2026-10-12", "2026-10-22", "C")
		_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
			Root:       root,
			Exceptions: []config.Exception{entry},
			Today:      releaseTokenToday,
		})
		if err == nil {
			t.Fatal("AuditReleaseToken passed; want failure for stale entry")
		}
		if !strings.Contains(err.Error(), "excuse no step") {
			t.Errorf("error = %q; want excuse no step", err.Error())
		}
	})

	t.Run("entry when no release-please step exists", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			".github/workflows/ci.yml": "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 1\n",
		})
		entry := releaseTokenEntry(".github/workflows/release.yml", "2026-10-12", "2026-10-22", "C")
		_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
			Root:       root,
			Exceptions: []config.Exception{entry},
			Today:      releaseTokenToday,
		})
		if err == nil {
			t.Fatal("AuditReleaseToken passed; want failure for stale entry")
		}
		if !strings.Contains(err.Error(), "no workflow holds a release-please step") {
			t.Errorf("error = %q; want no workflow holds a release-please step", err.Error())
		}
	})
}

// TestAuditReleaseToken_CategoryCOlderThan10Days verifies that ValidateExceptions enforces category C 10-day bound.
func TestAuditReleaseToken_CategoryCOlderThan10Days(t *testing.T) {
	root := supplyChainRepo(t, map[string]string{
		".github/workflows/release.yml": failingReleaseWorkflow,
	})
	// 20 days between entered and expires exceeds category C 10-day bound
	entry := releaseTokenEntry(".github/workflows/release.yml", "2026-10-01", "2026-10-21", "C")
	_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
		Root:       root,
		Exceptions: []config.Exception{entry},
		Today:      releaseTokenToday,
	})
	if err == nil {
		t.Fatal("AuditReleaseToken passed; want failure for category C duration violation")
	}
	if !strings.Contains(err.Error(), "category C") && !strings.Contains(err.Error(), "10 days") {
		t.Errorf("error = %q; want category C or 10 days message", err.Error())
	}
}

// TestAuditReleaseToken_Skip verifies that repositories without release-please skip the gate.
func TestAuditReleaseToken_Skip(t *testing.T) {
	t.Run("no workflows directory", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			"README.md": "readme",
		})
		out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
			Root:  root,
			Today: releaseTokenToday,
		})
		if err != nil {
			t.Fatalf("AuditReleaseToken = %v; want skip", err)
		}
		want := "[SKIP] Release token not checked: no workflow under .github/workflows holds a release-please step."
		if out != want {
			t.Errorf("output = %q; want %q", out, want)
		}
	})

	t.Run("workflows without release-please", func(t *testing.T) {
		root := supplyChainRepo(t, map[string]string{
			".github/workflows/ci.yml": "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n",
		})
		out, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
			Root:  root,
			Today: releaseTokenToday,
		})
		if err != nil {
			t.Fatalf("AuditReleaseToken = %v; want skip", err)
		}
		want := "[SKIP] Release token not checked: no workflow under .github/workflows holds a release-please step."
		if out != want {
			t.Errorf("output = %q; want %q", out, want)
		}
	})
}

// TestAuditReleaseToken_FailsWithoutExceptions verifies failure when an unexcepted failing step exists.
func TestAuditReleaseToken_FailsWithoutExceptions(t *testing.T) {
	root := supplyChainRepo(t, map[string]string{
		".github/workflows/release.yml": failingReleaseWorkflow,
	})
	_, err := AuditReleaseToken(context.Background(), ReleaseTokenOptions{
		Root:  root,
		Today: releaseTokenToday,
	})
	if err == nil {
		t.Fatal("AuditReleaseToken passed; want failure")
	}
	if !strings.Contains(err.Error(), "[FAIL] Release token:") {
		t.Errorf("error = %q; want [FAIL] Release token:", err.Error())
	}
	if !strings.Contains(err.Error(), "takes the workflow token (github.token or secrets.GITHUB_TOKEN)") {
		t.Errorf("error = %q; want the workflow-token finding", err.Error())
	}
}

// TestAuditReleaseToken_NilContext verifies that a nil context fails closed.
func TestAuditReleaseToken_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: deliberate nil context under test
	_, err := AuditReleaseToken(nil, ReleaseTokenOptions{Root: t.TempDir()})
	if err == nil {
		t.Fatal("AuditReleaseToken(nil context) passed; want error")
	}
	if !strings.Contains(err.Error(), "the audit requires a context") {
		t.Errorf("error = %q; want requires a context", err.Error())
	}
}
