package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretConfig = `{
  "mcpServers": {
    "gh": {
      "command": "npx",
      "args": ["-y", "server", "--header", "Authorization: Bearer abc123"],
      "env": {"GITHUB_TOKEN": "ghp_live", "PATH": "/usr/bin"},
      "headers": {"Authorization": "Bearer zzz", "Content-Type": "application/json"}
    }
  },
  "apiKey":   "sk-live",
  "password": "hunter2",
  "clientSecret": "cs-live",
  "maxTokens": 4096,
  "credentials": {"user": "a", "pass": ["b", {"c": 1}]},
  "tokenizer": "cl100k",
  "author": "someone"
}
`

const redactedConfig = `{
  "mcpServers": {
    "gh": {
      "command": "npx",
      "args": ["-y", "server", "--header", "[REDACTED]"],
      "env": {"GITHUB_TOKEN": "[REDACTED]", "PATH": "[REDACTED]"},
      "headers": {"Authorization": "[REDACTED]", "Content-Type": "application/json"}
    }
  },
  "apiKey":   "[REDACTED]",
  "password": "[REDACTED]",
  "clientSecret": "[REDACTED]",
  "maxTokens": 4096,
  "credentials": "[REDACTED]",
  "tokenizer": "cl100k",
  "author": "someone"
}
`

// Positive: token, secret, password, apiKey, Authorization, bearer and env values are
// replaced, and every other byte, whitespace and key order included, is unchanged (BUG-608).
func TestRedactJSONSecretsReplacesCredentialValues(t *testing.T) {
	got, count, err := redactJSONSecrets([]byte(secretConfig))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != redactedConfig {
		t.Fatalf("redaction changed more or less than the credential values:\n%s", got)
	}
	if count != 8 {
		t.Fatalf("redacted %d values, want 8", count)
	}
	for _, leaked := range []string{"abc123", "ghp_live", "zzz", "sk-live", "hunter2", "cs-live"} {
		if strings.Contains(string(got), leaked) {
			t.Fatalf("credential %q survived redaction", leaked)
		}
	}
}

// Negative: a file that is not valid JSON is refused, never passed through.
func TestRedactJSONSecretsRejectsMalformed(t *testing.T) {
	for _, body := range []string{"secret", `{"token": "x"`, `{"token": "x"} trailing`, ""} {
		if _, _, err := redactJSONSecrets([]byte(body)); !errors.Is(err, ErrMalformedConfig) {
			t.Fatalf("%q: want ErrMalformedConfig, got %v", body, err)
		}
	}
}

// Boundary: a document without credentials comes back byte-identical, a top-level array
// and a scalar document are walked, and only whole-word or suffix key matches count.
func TestRedactJSONSecretsBoundaries(t *testing.T) {
	cases := []struct {
		in, want string
		count    int
	}{
		{`{}`, `{}`, 0},
		{"{\n\t\"command\": \"node\",\n\t\"args\": [\"a\", 1, true, null]\n}", "{\n\t\"command\": \"node\",\n\t\"args\": [\"a\", 1, true, null]\n}", 0},
		{`["Basic dXNlcg==", "plain"]`, `["[REDACTED]", "plain"]`, 1},
		{`"Bearer x"`, `"[REDACTED]"`, 1},
		{`{"auth":"a","authority":"b","x-api-key":"c","token":null,"access_token":{}}`, `{"auth":"[REDACTED]","authority":"b","x-api-key":"[REDACTED]","token":null,"access_token":"[REDACTED]"}`, 3},
		{`{"a":{"b":[{"env":{"K":"v","N":2}}]}}`, `{"a":{"b":[{"env":{"K":"[REDACTED]","N":2}}]}}`, 1},
	}
	for _, tc := range cases {
		got, count, err := redactJSONSecrets([]byte(tc.in))
		if err != nil || string(got) != tc.want || count != tc.count {
			t.Errorf("%s: got %s (%d), %v; want %s (%d)", tc.in, got, count, err, tc.want, tc.count)
		}
	}
	if needsRedaction("cli-history", "/h/.bash_history") || needsRedaction("codex-config", "/h/.codex/config.toml") {
		t.Error("non-JSON files must be copied as written")
	}
	if !needsRedaction("claude-config", "/h/.claude.json.backup") || needsRedaction("agent-rule", "/h/x.json") {
		t.Error("redaction must follow the sensitive categories and JSON file names")
	}
}

// Positive and negative through the bundler: a credential-bearing config lands redacted
// with a record whose hash matches the bytes on disk and a redaction count on the report;
// a malformed config is skipped with a note and never copied.
func TestBundleRedactsAgentConfigs(t *testing.T) {
	home := t.TempDir()
	out := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), secretConfig)
	mustWriteFile(t, filepath.Join(home, ".gemini", "config", "mcp_config.json"), `{"env": {"TOKEN": "leak"`)
	mustWriteFile(t, filepath.Join(home, ".codex", "config.toml"), "token = \"kept-as-written\"\n")

	rep, err := BundleWorkstation(context.Background(), BundleOptions{Roots: testClientRoots(home), HomeDir: home, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	var settings *BundleFileRecord
	for i := range rep.Records {
		if rep.Records[i].RelativePath == "agent-configs/claude/settings.json" {
			settings = &rep.Records[i]
		}
		if strings.HasSuffix(rep.Records[i].RelativePath, "mcp_config.json") {
			t.Fatalf("a malformed config was bundled: %+v", rep.Records[i])
		}
	}
	if settings == nil || settings.Redacted != 8 {
		t.Fatalf("settings.json record missing or unredacted: %+v", settings)
	}
	data, err := os.ReadFile(filepath.Join(out, "agent-configs", "claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if string(data) != redactedConfig || hex.EncodeToString(sum[:]) != settings.SHA256 || int64(len(data)) != settings.SizeBytes {
		t.Fatal("bundled bytes are not the redacted config the manifest describes")
	}
	// settings.json is captured twice (agent-configs/ and agent-configs/claude/).
	if rep.RedactedValues != 16 {
		t.Fatalf("report totals %d redactions, want 16", rep.RedactedValues)
	}
	if !strings.Contains(strings.Join(rep.Skipped, "\n"), "not valid JSON") {
		t.Fatalf("malformed config skip not noted: %v", rep.Skipped)
	}
	toml, err := os.ReadFile(filepath.Join(out, "agent-configs", "codex", "config.toml"))
	if err != nil || string(toml) != "token = \"kept-as-written\"\n" {
		t.Fatalf("non-JSON config must be copied as written: %q, %v", toml, err)
	}
}
