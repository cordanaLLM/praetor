// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

func contextFixture(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sources")
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAnalyzeAndWritePreserveBytesAndScopes(t *testing.T) {
	private := bytes.Repeat([]byte("private policy: keep ALL checks.\r\n"), 100)
	unique := []byte("# Different skill with the same basename\n")
	files := map[string][]byte{"one/SKILL.md": private, "two/SKILL.md": private, "three/SKILL.md": unique}
	root := contextFixture(t, files)
	opts := Options{Root: root, Sources: []string{"one/SKILL.md", "two/SKILL.md", "three/SKILL.md"}}
	plan, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	report := plan.Metadata()
	if len(report.Documents) != 2 || report.SavedBytes <= 0 || !report.ReviewRequired {
		t.Fatalf("unexpected metadata: %+v", report)
	}
	if report.Sources[1].Reason != "exact_duplicate" || report.Sources[2].Reason != "retained" {
		t.Fatalf("lost source distinctions: %+v", report.Sources)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private policy")) {
		t.Fatal("report leaked contents")
	}
	output := filepath.Join(filepath.Dir(root), "candidate")
	if err := plan.WriteCandidate(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	pack, err := os.ReadFile(filepath.Join(output, "context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if digest(pack) != report.PackSHA256 || len(pack) != report.PackBytes || report.SavedBytes != report.InputBytes-len(pack) {
		t.Fatal("pack measurement or digest does not match actual bytes")
	}
	for _, doc := range report.Documents {
		if !bytes.Equal(pack[doc.Offset:doc.Offset+doc.Bytes], files[doc.SourcePaths[0]]) {
			t.Fatal("retained document bytes changed")
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("source changed: %s: %v", name, err)
		}
	}
	for _, name := range []string{".", "context.md", "manifest.json"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("non-private mode: %s", name)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted Report
	if err := json.Unmarshal(manifest, &persisted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, report) {
		t.Fatal("manifest differs from analysis")
	}
	if err := plan.WriteCandidate(context.Background(), output); err == nil {
		t.Fatal("existing candidate overwritten")
	}
	second, err := Analyze(context.Background(), opts)
	if err != nil || !reflect.DeepEqual(report, second.Metadata()) {
		t.Fatalf("nondeterministic analysis: %v", err)
	}
}

func TestCompilerProjectionRequiresExactCurrentCanonical(t *testing.T) {
	canonical := []byte("# Instructions\n\nPreserve all source scopes.\n")
	compiled, err := agentcontext.NewTranspiler().CompileContent(string(canonical))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"AGENTS.md": canonical, "copy.md": canonical, "stale/CLAUDE.md": []byte(compiled.Files[0].Content)}
	paths := []string{"copy.md"} // Canonical selection intentionally follows its projections.
	for _, target := range compiled.Files {
		files[target.RelativePath] = []byte(target.Content)
		paths = append(paths, target.RelativePath)
	}
	paths = append(paths, "AGENTS.md", "stale/CLAUDE.md")
	root := contextFixture(t, files)
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: paths})
	if err != nil {
		t.Fatal(err)
	}
	r := plan.Metadata()
	if len(r.Documents) != 2 || r.Documents[0].SHA256 != digest(canonical) {
		t.Fatalf("projection merge: %+v", r)
	}
	for i := 1; i <= len(compiled.Files); i++ {
		if r.Sources[i].RetainedPath != "AGENTS.md" || r.Sources[i].Reason != "verified_compiler_projection" {
			t.Fatalf("projection %d not verified: %+v", i, r.Sources[i])
		}
	}
	if r.Sources[len(r.Sources)-1].Reason != "retained" {
		t.Fatal("arbitrary nested CLAUDE.md treated as root compiler projection")
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), append(files["CLAUDE.md"], ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	drift, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"AGENTS.md", "CLAUDE.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Metadata().Documents) != 2 {
		t.Fatal("whitespace drift silently dropped")
	}
	without, err := Analyze(context.Background(), Options{Root: root, Sources: []string{".windsurfrules", ".cursor/rules/hiss-invariants.mdc"}})
	if err != nil || len(without.Metadata().Documents) != 2 {
		t.Fatalf("projection inferred without canonical: %v", err)
	}
}

func TestAnalyzeReportsUniquePackGrowthHonestly(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"empty.md": {}, "unique.md": []byte("unique")})
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"empty.md", "unique.md"}})
	if err != nil {
		t.Fatal(err)
	}
	r := plan.Metadata()
	if r.SavedBytes >= 0 || r.PayloadBytes != r.InputBytes || len(r.Documents) != 2 {
		t.Fatalf("false savings: %+v", r)
	}
}

func TestAnalyzeRejectsInvalidSourcesWithoutPartialPlan(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"ok.md": []byte("policy"), "binary.md": {0}, "invalid.md": {0xff}})
	for _, sources := range [][]string{nil, {"ok.md", "missing.md"}, {"../ok.md"}, {"./ok.md"}, {"ok.md", "ok.md"}, {"binary.md"}, {"invalid.md"}, {root}, {"."}, {"new\nline"}, {strings.Repeat("x", MaxPathBytes+1)}} {
		plan, err := Analyze(context.Background(), Options{Root: root, Sources: sources})
		if err == nil || plan != nil {
			t.Fatalf("invalid selection accepted: %q", sources)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(ctx, Options{Root: root, Sources: []string{"ok.md"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestAnalyzeSourceAndTotalBoundaries(t *testing.T) {
	files := make(map[string][]byte)
	paths := make([]string, MaxSources)
	for i := 0; i < MaxSources; i++ {
		paths[i] = fmt.Sprintf("source-%d.md", i)
		files[paths[i]] = []byte("a")
	}
	root := contextFixture(t, files)
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: paths}); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: append(paths, "missing.md")}); err == nil {
		t.Fatal("source count overflow accepted")
	}
	for i := 0; i < MaxTotalBytes/MaxSourceBytes; i++ {
		if err := os.WriteFile(filepath.Join(root, paths[i]), bytes.Repeat([]byte("a"), MaxSourceBytes), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exact := paths[:MaxTotalBytes/MaxSourceBytes]
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: exact}); err != nil {
		t.Fatalf("exact total bound: %v", err)
	}
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: paths[:len(exact)+1]}); err == nil {
		t.Fatal("total byte overflow accepted")
	}
	if err := os.WriteFile(filepath.Join(root, paths[0]), bytes.Repeat([]byte("a"), MaxSourceBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: paths[:1]}); err == nil {
		t.Fatal("source byte overflow accepted")
	}
}

func TestWriteRejectsChangedSourcesCancellationAndOutputOverlap(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"policy.md": []byte("original")})
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"policy.md"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"", root, filepath.Join(root, "policy.md"), filepath.Join(root, "policy.md", "output"), filepath.Dir(root)} {
		if err := plan.WriteCandidate(context.Background(), output); err == nil {
			t.Fatalf("overlap accepted: %s", output)
		}
	}
	output := filepath.Join(filepath.Dir(root), "candidate")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := plan.WriteCandidate(ctx, output); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "policy.md"), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteCandidate(context.Background(), output); err == nil {
		t.Fatal("stale snapshot written")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preflight created output: %v", err)
	}
}

func TestStableReadRejectsChangedAndReplacedSources(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"policy.md": []byte("before")})
	path := filepath.Join(root, "policy.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after length changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stableRead(context.Background(), file, before); err == nil {
		t.Fatal("changed file accepted")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new inode"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stableRead(context.Background(), file, before); err == nil {
		t.Fatal("replaced file accepted")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeAndOutputRejectSymlinks(t *testing.T) {
	root := contextFixture(t, map[string][]byte{"policy.md": []byte("keep")})
	if err := os.Symlink("policy.md", filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, target := range []string{"policy.md", t.TempDir()} {
		link := filepath.Join(root, "dir-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"dir-link/secret.md"}}); err == nil {
			t.Fatal("directory symlink accepted")
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"link.md"}}); err == nil {
		t.Fatal("file symlink accepted")
	}
	linkedRoot := filepath.Join(filepath.Dir(root), "root-link")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), Options{Root: linkedRoot, Sources: []string{"policy.md"}}); err == nil {
		t.Fatal("root symlink accepted")
	}
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"policy.md"}})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(filepath.Dir(root), "output-link")
	if err := os.Symlink(t.TempDir(), out); err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteCandidate(context.Background(), out); err == nil {
		t.Fatal("output symlink accepted")
	}
}

func TestExplicitOutputInsideBroadRootAndDetachedMetadata(t *testing.T) {
	root := contextFixture(t, map[string][]byte{".codex/AGENTS.md": []byte("policy"), ".local/state/marker": {}})
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: []string{".codex/AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := plan.Metadata()
	metadata.Sources[0].Path = "forged"
	metadata.Documents[0].SourcePaths[0] = "forged"
	output := filepath.Join(root, ".local", "state", "candidate")
	if err := plan.WriteCandidate(context.Background(), output); err != nil {
		t.Fatalf("explicit home-local output rejected: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifest, []byte("forged")) {
		t.Fatal("metadata caller mutated internal plan")
	}
	var missingContext context.Context // Negative input: neither public entry point may panic.
	if _, err := Analyze(missingContext, Options{Root: root, Sources: []string{".codex/AGENTS.md"}}); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := plan.WriteCandidate(missingContext, output); err == nil {
		t.Fatal("nil write context accepted")
	}
	if err := (*Plan)(nil).WriteCandidate(context.Background(), output); err == nil {
		t.Fatal("nil plan accepted")
	}
}

func TestPathDepthAndCompilerBudgetFailClosed(t *testing.T) {
	exact := strings.Repeat("x/", MaxPathDepth-1) + "x"
	if err := validatePath(exact); err != nil {
		t.Fatalf("exact depth: %v", err)
	}
	if err := validatePath(exact + "/x"); err == nil {
		t.Fatal("depth overflow accepted")
	}
	canonical := strings.Repeat("policy\n", agentcontext.MaxLineBudget+1)
	root := contextFixture(t, map[string][]byte{"AGENTS.md": []byte(canonical), "CLAUDE.md": []byte("unverified projection")})
	plan, err := Analyze(context.Background(), Options{Root: root, Sources: []string{"AGENTS.md", "CLAUDE.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Metadata().Documents) != 2 {
		t.Fatal("unverifiable projection discarded")
	}
}
