package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

const scheduleClaudeRecord = `{"type":"user","uuid":"record-id","parentUuid":"parent-id","sessionId":"session-id","timestamp":"2026-09-12T12:00:00.123Z","message":{"role":"user","content":"synthetic prompt"}}`

func scheduleTranscriptSource(t *testing.T, record string) (string, *ScheduleConfig, SuiteTranscript) {
	t.Helper()
	requireScheduling(t)
	path, cfg := scheduleFixture(t, record)
	suite, _, err := loadSuiteConfig(context.Background(), cfg.SuiteConfig)
	if err != nil {
		t.Fatal(err)
	}
	return path, cfg, suite.Transcripts[0]
}

func scheduleTranscriptFixture(t *testing.T, id, name, format, content string) SuiteTranscript {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	data := []byte(content)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return SuiteTranscript{
		ID:         id,
		SourcePath: path,
		SHA256:     hex.EncodeToString(sum[:]),
		Format:     format,
	}
}

func scheduleTranscriptPair(t *testing.T) (string, *ScheduleConfig, []SuiteTranscript) {
	t.Helper()
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	sources := []SuiteTranscript{
		scheduleTranscriptFixture(
			t,
			"claude",
			"123e4567-e89b-12d3-a456-426614174000.jsonl",
			"claude-code-jsonl-v1",
			scheduleClaudeRecord+"\n",
		),
		scheduleTranscriptFixture(
			t,
			"antigravity",
			"transcript_full.jsonl",
			"antigravity-jsonl-v1",
			suiteFixtureRecord+"\n",
		),
	}
	writeScheduleFixture(t, cfg.SuiteConfig, SuiteConfig{
		Version:            1,
		PublicRepositories: []string{},
		Transcripts:        sources,
	})
	return path, cfg, sources
}

func transcriptStorePath(cfg *ScheduleConfig, source SuiteTranscript) string {
	dir := source.ID + "-" + source.SHA256
	return filepath.Join(cfg.StateDir, "transcripts", dir, filepath.Base(source.SourcePath))
}

func readRunSuite(t *testing.T, cfg *ScheduleConfig, run int) *SuiteConfig {
	t.Helper()
	path := filepath.Join(cfg.StateDir, scheduleRunName(run), "inputs", "suite.json")
	suite, _, err := loadSuiteConfig(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

func assertScheduleReplayPaths(t *testing.T, report *ScheduleReport, suite *SuiteConfig) {
	t.Helper()
	if report.Suite == nil || len(report.Suite.Cases) != len(suite.Transcripts) {
		t.Fatalf("suite report cases: %+v", report.Suite)
	}
	for i := 0; i < len(suite.Transcripts) && i < MaxSuiteCases; i++ {
		want := suite.Transcripts[i].SourcePath
		result := report.Suite.Cases[i]
		if result.Transcript == nil || result.Transcript.SourcePath != want {
			t.Fatalf("case %d transcript path: %+v, want %q", i, result.Transcript, want)
		}
		for _, pass := range []*SuiteReplayPass{result.Ingestion, result.Replay} {
			if pass == nil {
				t.Fatalf("case %d missing replay pass", i)
			}
			for page := 0; page < len(pass.Pages) && page < maxSuitePages; page++ {
				if got := pass.Pages[page].Source.Path; got != want {
					t.Fatalf("case %d page %d path %q, want suite path %q", i, page, got, want)
				}
			}
		}
	}
}

func TestScheduleTranscriptStoreSurvivesSourcePruning(t *testing.T) {
	path, cfg, sources := scheduleTranscriptPair(t)
	runner := suiteRunnerAs(buildid.Identity{Release: "test-schedule-transcript-store"})

	first, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, runner)
	if err != nil || !first.Verified {
		t.Fatalf("first run: %+v %v", first, err)
	}
	storeInfo, err := os.Stat(filepath.Join(cfg.StateDir, scheduleTranscriptDir))
	if err != nil || storeInfo.Mode().Perm() != 0o700 {
		t.Fatalf("transcript store mode: %v %v", storeInfo, err)
	}
	firstSuite := readRunSuite(t, cfg, 1)
	for i := 0; i < len(sources) && i < MaxSuiteCases; i++ {
		store := transcriptStorePath(cfg, sources[i])
		if firstSuite.Transcripts[i].SourcePath != store {
			t.Fatalf("first run source_path %q, want %q", firstSuite.Transcripts[i].SourcePath, store)
		}
		info, statErr := os.Stat(store)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("stored transcript mode: %v %v", info, statErr)
		}
		dirInfo, statErr := os.Stat(filepath.Dir(store))
		if statErr != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("transcript directory mode: %v %v", dirInfo, statErr)
		}
		if removeErr := os.Remove(sources[i].SourcePath); removeErr != nil {
			t.Fatal(removeErr)
		}
	}

	later := func() time.Time { return fixedScheduleTime().Add(2 * time.Hour) }
	second, err := scheduleTick(context.Background(), path, true, later, runner)
	if err != nil || !second.Verified {
		t.Fatalf("second run after source pruning: %+v %v", second, err)
	}
	assertScheduleReplayPaths(t, second, readRunSuite(t, cfg, 2))
}

func TestScheduleTranscriptStoreRejectsInvalidBaseName(t *testing.T) {
	source := SuiteTranscript{ID: "unsafe"}
	invalid := []string{"nested/name.jsonl", `nested\name.jsonl`, "..", strings.Repeat("a", 256)}
	for _, name := range invalid {
		if err := validateScheduleTranscriptBaseName(source, name); err == nil || !strings.Contains(err.Error(), source.ID) {
			t.Errorf("invalid base name %q not refused with transcript ID: %v", name, err)
		}
	}
	if err := validateScheduleTranscriptBaseName(source, strings.Repeat("a", 255)); err != nil {
		t.Fatalf("255-byte base name refused: %v", err)
	}
}

func TestScheduleTranscriptStoreFailsClosedWithoutDeclaredBytes(t *testing.T) {
	t.Run("missing source and store", func(t *testing.T) {
		path, cfg, source := scheduleTranscriptSource(t, suiteFixtureRecord+"\n")
		store := transcriptStorePath(cfg, source)
		if err := os.Remove(source.SourcePath); err != nil {
			t.Fatal(err)
		}
		_, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner)
		if err == nil || !strings.Contains(err.Error(), source.ID) ||
			!strings.Contains(err.Error(), source.SourcePath) || !strings.Contains(err.Error(), store) {
			t.Fatalf("error did not name transcript and both paths: %v", err)
		}
	})

	t.Run("source hash mismatch", func(t *testing.T) {
		path, cfg, source := scheduleTranscriptSource(t, suiteFixtureRecord+"\n")
		store := transcriptStorePath(cfg, source)
		if err := os.WriteFile(source.SourcePath, []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner)
		if err == nil || !strings.Contains(err.Error(), source.ID) ||
			!strings.Contains(err.Error(), source.SourcePath) || !strings.Contains(err.Error(), store) {
			t.Fatalf("hash error did not name transcript and both paths: %v", err)
		}
		if _, statErr := os.Stat(store); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("mismatched source was stored: %v", statErr)
		}
	})
}

func TestScheduleTranscriptStoreRefusesTampering(t *testing.T) {
	path, cfg, source := scheduleTranscriptSource(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	store := transcriptStorePath(cfg, source)
	tampered := []byte("tampered\n")
	if err := os.WriteFile(store, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return fixedScheduleTime().Add(2 * time.Hour) }
	_, err := scheduleTick(context.Background(), path, true, later, verifiedScheduleRunner)
	if err == nil || !strings.Contains(err.Error(), store) {
		t.Fatalf("tampered store was not named: %v", err)
	}
	got, readErr := os.ReadFile(store)
	if readErr != nil || string(got) != string(tampered) {
		t.Fatalf("tampered store was replaced: %q %v", got, readErr)
	}
}

func TestScheduleTranscriptStorePinsIDAndHash(t *testing.T) {
	path, cfg, first := scheduleTranscriptSource(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	secondBytes := []byte(strings.ReplaceAll(
		suiteFixtureRecord,
		"untrusted fixture observation",
		"second synthetic observation",
	) + "\n")
	if err := os.WriteFile(first.SourcePath, secondBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	secondSum := sha256.Sum256(secondBytes)
	second := first
	second.SHA256 = hex.EncodeToString(secondSum[:])
	writeScheduleFixture(t, cfg.SuiteConfig, SuiteConfig{
		Version:            1,
		PublicRepositories: []string{},
		Transcripts:        []SuiteTranscript{second},
	})
	later := func() time.Time { return fixedScheduleTime().Add(2 * time.Hour) }
	if _, err := scheduleTick(context.Background(), path, true, later, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(transcriptStorePath(cfg, first)) == filepath.Dir(transcriptStorePath(cfg, second)) {
		t.Fatal("different hashes shared a transcript directory")
	}
	for _, source := range []SuiteTranscript{first, second} {
		if _, err := os.Stat(transcriptStorePath(cfg, source)); err != nil {
			t.Fatalf("missing store for %s: %v", source.SHA256, err)
		}
	}
}

func TestScheduleTranscriptStoreUsesExistingFileWithoutRewrite(t *testing.T) {
	path, cfg, source := scheduleTranscriptSource(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	store := transcriptStorePath(cfg, source)
	pinnedTime := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(store, pinnedTime, pinnedTime); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return fixedScheduleTime().Add(2 * time.Hour) }
	if _, err := scheduleTick(context.Background(), path, true, later, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store)
	if err != nil || !info.ModTime().Equal(pinnedTime) {
		t.Fatalf("stored transcript was rewritten: %v %v", info, err)
	}
	if got := readRunSuite(t, cfg, 2).Transcripts[0].SourcePath; got != store {
		t.Fatalf("run source_path %q, want %q", got, store)
	}
}
