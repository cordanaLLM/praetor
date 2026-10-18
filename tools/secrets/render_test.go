// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

const sampleWorkflow = `name: Security
jobs:
  scan:
    steps:
      - name: Run Secrets Scan (gitleaks)
        run: go run -tags=secretsgate ./tools/secrets/gate
`

func TestRenderWorkflow_RoundTrip_3D(t *testing.T) {
	// Empty entries: plain unchanged
	out, err := secrets.RenderWorkflow(sampleWorkflow, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != sampleWorkflow {
		t.Errorf("empty entries must return plain unchanged")
	}

	entries := []rules.Entry{
		{
			Path:    "creds.py",
			RuleID:  "generic-api-key",
			Expires: "2026-11-01",
			Reason:  "test waiver",
		},
	}

	rendered, err := secrets.RenderWorkflow(sampleWorkflow, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, secrets.ExceptionsEnv) {
		t.Errorf("expected rendered workflow to contain %s", secrets.ExceptionsEnv)
	}

	plain, ok := secrets.StripSettings(rendered)
	if !ok {
		t.Fatal("StripSettings failed on rendered workflow")
	}
	if plain != sampleWorkflow {
		t.Errorf("StripSettings round-trip mismatch:\ngot:\n%s\nwant:\n%s", plain, sampleWorkflow)
	}
}

func TestRenderWorkflow_NegativeMissingMarker(t *testing.T) {
	badWorkflow := `name: Security
jobs:
  scan:
    steps:
      - run: gitleaks detect
`
	entries := []rules.Entry{{Path: "a", RuleID: "b", Expires: "2026-11-01", Reason: "c"}}
	_, err := secrets.RenderWorkflow(badWorkflow, entries)
	if err == nil {
		t.Fatal("expected error when gate step is missing, got nil")
	}
}
