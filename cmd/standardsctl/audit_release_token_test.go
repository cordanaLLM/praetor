package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func fixtureFailingReleaseWorkflow() string {
	return `on: push
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ github.token }}
`
}

func fixturePassingReleaseWorkflow() string {
	return `on: push
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`
}

// TestAuditReleaseToken_CLI_RequiresAppToken tests the release-token gate in standardsctl audit.
func TestAuditReleaseToken_CLI_RequiresAppToken(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[SKIP] Release token not checked: no workflow under .github/workflows holds a release-please step.")

	writeFixtureFile(t, f.dir, ".github/workflows/release.yml", fixtureFailingReleaseWorkflow())
	out, err = f.audit(t)
	if err == nil {
		t.Fatalf("audit passed release-please with github.token:\n%s", out)
	}
	mustErrContain(t, err, "[FAIL] Release token:")

	writeFixtureFile(t, f.dir, ".github/workflows/release.yml", fixturePassingReleaseWorkflow())
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed with valid app-token setup: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Release token: 1 release-please step takes token from a GitHub App token step.")

	writeFixtureFile(t, f.dir, ".github/workflows/release.yml", fixtureFailingReleaseWorkflow())
	today := time.Now()
	entered := today.Format(config.ExceptionDateLayout)
	expires := today.AddDate(0, 0, 10).Format(config.ExceptionDateLayout)
	entry := "exceptions:\n  - rule: release-token\n    path: .github/workflows/release.yml\n" +
		"    category: C\n    entered: \"%s\"\n    reason: app token migration underway\n    expires: \"%s\"\n"
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+fmt.Sprintf(entry, entered, expires))
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed with a live release-token entry: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "excepted until "+expires+" by the exceptions entry (rule release-token, .github/workflows/release.yml)")

	// Expired entry
	expiredDate := today.AddDate(0, 0, -5).Format(config.ExceptionDateLayout)
	expiredEntered := today.AddDate(0, 0, -15).Format(config.ExceptionDateLayout)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+fmt.Sprintf(entry, expiredEntered, expiredDate))
	if out, err = f.audit(t); err == nil {
		t.Fatalf("audit passed with an expired release-token entry:\n%s", out)
	}
	mustErrContain(t, err, "expired on "+expiredDate)
}
