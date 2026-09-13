package dogfood

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicEnrollmentAcceptsExplicitPinnedGitHubRepositories(t *testing.T) {
	inputs := []string{
		"https://github.com/jellysin/plugin-lastfm#37f446e7d7def05891ef2f4878f9512be3668ec7",
		"https://github.com/jellysin/release-helper#41f9ccffa1093d7dda8c3f7f634fad24d08cdc7a",
		"https://github.com/Example-Team/.github#" + strings.Repeat("a", 64),
	}
	sources, err := parsePublicSources(inputs)
	if err != nil || len(sources) != len(inputs) {
		t.Fatalf("explicit pinned repositories rejected: %+v %v", sources, err)
	}
	for i, source := range sources {
		if source.url+"#"+source.sha != inputs[i] {
			t.Fatalf("enrolled source identity changed: %+v", source)
		}
	}
}

func TestPublicEnrollmentRejectsAmbiguousURLs(t *testing.T) {
	for _, input := range []string{
		"http://github.com/example/repo", "https://gitlab.com/example/repo", "https://github.com.evil.test/example/repo",
		"https://user:secret@github.com/example/repo", "https://github.com:443/example/repo", "https://github.com./example/repo",
		"https://GITHUB.COM/example/repo", "HTTPS://github.com/example/repo", "git@github.com:example/repo",
		"https://github.com/example/repo?token=value", "https://github.com/example/repo/", "https://github.com/example/repo.git",
		"https://github.com/example/repo.GIT", "https://github.com/example/../repo", "https://github.com/example//repo",
		"https://github.com/example/repo/tree/main", "https://github.com/example/%2Frepo", "https://github.com/example/repo%23fragment",
		"https://github.com/example/repo%5cpath", "https://github.com/example/repo\\path", "https://github.com/example/.", "https://github.com/example/..",
		"https://github.com/-example/repo", "https://github.com/example-/repo", "https://github.com/example_name/repo",
		"https://github.com/example/repö", " https://github.com/spf13/cobra", "https://github.com/spf13/cobra\n",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := parsePublicSources([]string{input + "#" + strings.Repeat("a", 40)}); !errors.Is(err, ErrInvalidRepoURL) {
				t.Fatalf("ambiguous URL was not rejected: %v", err)
			}
		})
	}
}

func TestPublicEnrollmentRequiresPinsAndUniqueIdentity(t *testing.T) {
	if _, err := parsePublicSources([]string{"https://github.com/example/repo"}); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("additional repository lacks explicit pin requirement: %v", err)
	}
	inputs := []string{"https://github.com/spf13/cobra#" + strings.Repeat("a", 40), "https://github.com/SPF13/COBRA#" + strings.Repeat("b", 40)}
	if _, err := parsePublicSources(inputs); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("case aliases at different pins were not rejected as duplicates: %v", err)
	}
	for _, pin := range []string{"", "main", strings.Repeat("A", 40), strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("a", 65), strings.Repeat("a", 40) + "#extra"} {
		if _, err := parsePublicSources([]string{"https://github.com/example/repo#" + pin}); !errors.Is(err, ErrInvalidRepoURL) {
			t.Fatalf("invalid pin accepted: %q %v", pin, err)
		}
	}
}

func TestPublicEnrollmentSourceBounds(t *testing.T) {
	if sources, err := parsePublicSources(nil); err != nil || len(sources) != 0 {
		t.Fatalf("transcript-only suite cannot omit public sources: %v", err)
	}
	inputs := make([]string, 8)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("https://github.com/example/repo%d#%s", i, strings.Repeat("a", 40))
	}
	if sources, err := parsePublicSources(inputs); err != nil || len(sources) != 8 {
		t.Fatalf("exact repository count bound rejected: %v", err)
	}
	if _, err := parsePublicSources(append(inputs, inputs[0])); err == nil {
		t.Fatal("source parser silently truncated over-bound enrollment")
	}
	for _, test := range []struct {
		owner, repo int
		valid       bool
	}{{39, 100, true}, {40, 100, false}, {39, 101, false}} {
		input := "https://github.com/" + strings.Repeat("a", test.owner) + "/" + strings.Repeat("b", test.repo) + "#" + strings.Repeat("c", 40)
		if _, err := parsePublicSources([]string{input}); (err == nil) != test.valid {
			t.Fatalf("owner/repository bound %d/%d: %v", test.owner, test.repo, err)
		}
	}
}

func TestSuitePlansJellysinEnrollmentWithoutRemotePermission(t *testing.T) {
	root := t.TempDir()
	config := SuiteConfig{Version: 1, PublicRepositories: []string{"https://github.com/jellysin/plugin-lastfm#37f446e7d7def05891ef2f4878f9512be3668ec7"}, Transcripts: []SuiteTranscript{}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "suite.json")
	publicWrite(t, path, string(data), 0o600)
	opts := SuiteOptions{ConfigPath: path, SourceRoot: root, ArtifactDir: filepath.Join(root, "plan"), Stage: "plan"}
	report, err := RunSuite(t.Context(), opts)
	if err != nil || report.Verified || report.Status != "planned" || report.Cases[0].Repository != config.PublicRepositories[0] {
		t.Fatalf("explicit enrollment did not remain an unverified plan: %+v %v", report, err)
	}
	opts.Stage, opts.ArtifactDir = "verify", filepath.Join(root, "verify")
	if _, err := RunSuite(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("explicit enrollment bypassed remote permission: %v", err)
	}
	if _, err := os.Stat(opts.ArtifactDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("denied verification created evidence: %v", err)
	}
}
