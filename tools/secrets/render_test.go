// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// RenderWorkflow and StripSettings still work on the unchanged emitted workflow with one HISS-12 exception.
func TestRenderWorkflow_EmittedSecretsExceptionsCompatibility(t *testing.T) {
	path := filepath.Join("..", "..", "templates", "testdata", "rendered", "go", "security-go.yml", "fallback.yml")
	// nosemgrep: go_filesystem_rule-fileread -- path is a fixed fixture path under templates/testdata, not input
	plain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := []rules.Entry{{Path: "creds.py", RuleID: "generic-api-key", Category: "C",
		Entered: "2026-11-10", Expires: "2026-11-20", Reason: "renderer compatibility fixture"}}
	rendered, err := secrets.RenderWorkflow(string(plain), entries)
	if err != nil {
		t.Fatalf("emitted fallback must accept HISS-12 exceptions: %v", err)
	}
	if strings.Count(rendered, "        env:\n") != strings.Count(string(plain), "        env:\n")+1 || strings.Count(rendered, secrets.ExceptionsEnv) != 1 {
		t.Fatal("expected one added exceptions environment in the unchanged gate step")
	}
	_, tail, found := strings.Cut(rendered, secrets.ExceptionsEnv+": ")
	if !found {
		t.Fatal("missing exceptions payload")
	}
	quoted, _, _ := strings.Cut(tail, "\n")
	var payload string
	if err := json.Unmarshal([]byte(quoted), &payload); err != nil {
		t.Fatal(err)
	}
	var decoded []rules.Entry
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, entries) {
		t.Fatalf("decoded exceptions: got %+v, want %+v", decoded, entries)
	}
	stripped, ok := secrets.StripSettings(rendered)
	if !ok || stripped != string(plain) {
		t.Fatal("emitted workflow exceptions must round-trip to the unchanged golden")
	}
}

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
