package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const suiteFixtureRecord = `{"step_index":1,"source":"MODEL","type":"MESSAGE","status":"DONE","created_at":"2026-09-12T12:00:00Z","content":"untrusted fixture observation"}`

func suiteFixture(t *testing.T, content string) SuiteTranscript {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return SuiteTranscript{ID: "fixture", SourcePath: path, SHA256: hex.EncodeToString(sum[:]), Format: "antigravity-jsonl-v1"}
}

func suiteOptions(t *testing.T, sources ...SuiteTranscript) SuiteOptions {
	t.Helper()
	config := SuiteConfig{Version: 1, PublicRepositories: []string{}, Transcripts: sources}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "suite.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return SuiteOptions{ConfigPath: path, ArtifactDir: filepath.Join(root, "run"), Stage: "verify"}
}

func TestSuiteCompleteReplayAndPagination(t *testing.T) {
	for _, records := range []int{1, 10001} {
		source := suiteFixture(t, strings.Repeat(suiteFixtureRecord+"\n", records))
		opts := suiteOptions(t, source)
		report, err := RunSuite(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		result := report.Cases[0]
		if !report.Verified || report.Status != "verified" || result.Status != "verified" {
			t.Fatalf("wrong status: %+v", report)
		}
		if result.Ingestion.Stored != records || result.Replay.Stored != 0 || result.Replay.AlreadyPresent != records || !result.Replay.Complete {
			t.Fatalf("wrong replay counters: %+v", result)
		}
		if len(result.Ingestion.Pages) != (records+9999)/10000 {
			t.Fatal("pagination skipped")
		}
		assertSuitePrivate(t, opts.ArtifactDir)
		original, err := os.ReadFile(source.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(original)
		if hex.EncodeToString(sum[:]) != source.SHA256 {
			t.Fatal("source changed")
		}
		if _, err := RunSuite(context.Background(), opts); err == nil {
			t.Fatal("existing evidence overwritten")
		}
	}
}

func assertSuitePrivate(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if util.ModeIsProtection() && info.Mode().Perm()&0o077 != 0 {
			t.Errorf("nonprivate evidence: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSuitePlanDoesNotReadSources(t *testing.T) {
	source := SuiteTranscript{ID: "missing", SourcePath: filepath.Join(t.TempDir(), "missing.jsonl"), SHA256: strings.Repeat("a", 64), Format: "claude-code-jsonl-v1"}
	opts := suiteOptions(t, source)
	opts.Stage = "plan"
	report, err := RunSuite(context.Background(), opts)
	if err != nil || report.Verified || report.Status != "planned" || report.Cases[0].Ingestion != nil {
		t.Fatalf("plan: %+v %v", report, err)
	}
	entries, err := os.ReadDir(opts.ArtifactDir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("plan wrote case data: %v %v", entries, err)
	}
}

func TestSuiteFailureStillReportsLaterCases(t *testing.T) {
	for _, content := range []string{"", strings.Replace(suiteFixtureRecord, `"content":"untrusted fixture observation"`, `"thinking":"excluded"`, 1), strings.TrimSuffix(suiteFixtureRecord, "}") + `,"truncated_fields":["content"]}`} {
		bad := suiteFixture(t, content)
		good := suiteFixture(t, suiteFixtureRecord)
		good.ID = "good"
		opts := suiteOptions(t, bad, good)
		report, err := RunSuite(context.Background(), opts)
		if err == nil || report.Verified || report.Cases[0].Status != "failed" || report.Cases[1].Status != "verified" {
			t.Fatalf("partial: %+v %v", report, err)
		}
		if strings.Contains(content, "truncated_fields") && report.Cases[0].Ingestion.Stored != 1 {
			t.Fatal("actual writes hidden by policy failure")
		}
		if _, err := os.Stat(filepath.Join(opts.ArtifactDir, "report.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSuiteStrictConfigurationBeforeWrites(t *testing.T) {
	source := suiteFixture(t, suiteFixtureRecord)
	valid := suiteOptions(t, source)
	data, err := os.ReadFile(valid.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(string(data), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(data), `"version"`, `"Version"`, 1),
		strings.Replace(string(data), `"version":1`, `"version":null`, 1),
		strings.Replace(string(data), `"public_repositories":[]`, `"public_repositories":null`, 1),
		strings.Replace(string(data), `"id":"fixture"`, `"id":"fixture","id":"again"`, 1),
		strings.Replace(string(data), source.SHA256, strings.ToUpper(source.SHA256), 1),
		// The config is JSON, so the path appears escaped in it: a Windows path's
		// backslashes are doubled there while source.SourcePath holds them single.
		// Replacing the raw form matched nothing on Windows and left the valid
		// configuration in place, so this "invalid" case tested a valid one.
		strings.Replace(string(data), jsonStringContent(t, source.SourcePath), "relative.jsonl", 1),
		`{"version":1,"public_repositories":[],"transcripts":[]}`,
		`{"version":1,"public_repositories":["https://github.com/spf13/cobra"],"transcripts":[]}`,
		string(data) + "{}",
	} {
		// A mutation that changes nothing turns a negative case into a positive one and
		// reports the wrong failure. Refuse it here rather than let it pass or fail for a
		// reason unrelated to the rule under test.
		if invalid == string(data) {
			t.Fatalf("invalid configuration is identical to the valid one; the mutation did not apply")
		}
		opts := valid
		opts.ConfigPath = filepath.Join(t.TempDir(), "invalid.json")
		opts.ArtifactDir = filepath.Join(t.TempDir(), "absent")
		if err := os.WriteFile(opts.ConfigPath, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if report, err := RunSuite(context.Background(), opts); err == nil || report != nil {
			t.Fatalf("accepted invalid configuration: %s", invalid)
		}
		if _, err := os.Stat(opts.ArtifactDir); !os.IsNotExist(err) {
			t.Fatal("invalid suite created evidence")
		}
	}
}

func TestSuiteBoundsPolicyAndCancellation(t *testing.T) {
	source := suiteFixture(t, suiteFixtureRecord)
	config := SuiteConfig{Version: 1}
	for i := 0; i < MaxSuiteCases; i++ {
		item := source
		item.ID = strings.Repeat("a", i+1)
		item.SourcePath = filepath.Join(t.TempDir(), "unread.jsonl")
		config.Transcripts = append(config.Transcripts, item)
	}
	if err := validateSuiteConfig(&config); err != nil {
		t.Fatal(err)
	}
	config.Transcripts = append(config.Transcripts, source)
	if err := validateSuiteConfig(&config); err == nil {
		t.Fatal("accepted nine cases")
	}
	opts := suiteOptions(t, source)
	opts.InputRoot = t.TempDir()
	if _, err := RunSuite(context.Background(), opts); err == nil {
		t.Fatal("embedded source escaped input root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunSuite(ctx, opts); err == nil {
		t.Fatal("ignored cancellation")
	}
	var absent context.Context
	if _, err := RunSuite(absent, opts); err == nil {
		t.Fatal("accepted nil context")
	}
	config = SuiteConfig{Version: 1, PublicRepositories: []string{"https://github.com/spf13/cobra#" + strings.Repeat("a", 40)}}
	opts.Stage, opts.SourceRoot = "verify", "."
	if err := validateSuitePolicy(&config, opts); err == nil {
		t.Fatal("remote opt-in bypass")
	}
	source.ID = "public-01"
	config.Transcripts = []SuiteTranscript{source}
	if err := validateSuiteConfig(&config); err == nil {
		t.Fatal("duplicate generated ID")
	}
}

// jsonStringContent returns value as it appears inside a JSON string literal, without the
// surrounding quotes. Search keys for substitutions into serialized JSON have to use this
// form: a raw value containing a backslash does not occur in the encoded document.
func jsonStringContent(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded[1 : len(encoded)-1])
}
