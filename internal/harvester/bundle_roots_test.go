package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testClientRoots lays the per-OS locations under a mock home. The real values come
// from internal/clientsetup through the command layer; this package cannot import it.
func testClientRoots(home string) ClientRoots {
	return ClientRoots{
		AGYConfig:           filepath.Join(home, ".gemini", "config"),
		AGYBrains:           []string{filepath.Join(home, ".gemini", "antigravity", "brain")},
		ClaudeDesktopConfig: filepath.Join(home, "desktop", "claude_desktop_config.json"),
		PowerShellHistory:   filepath.Join(home, "desktop", "ConsoleHost_history.txt"),
	}
}

func writeTranscript(t *testing.T, brain, conversation, content string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(brain, conversation, ".system_generated", "logs", "transcript.jsonl"), content)
}

func bundledPaths(report *WorkstationBundleReport) []string {
	paths := make([]string, 0, len(report.Records))
	for _, record := range report.Records {
		paths = append(paths, record.RelativePath)
	}
	return paths
}

func TestBundleReadsEveryBrainRootAndReportsIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	first := filepath.Join(home, ".gemini", "antigravity", "brain")
	second := filepath.Join(home, ".gemini", "antigravity-ide", "brain")
	writeTranscript(t, first, "conv-a", "first")
	writeTranscript(t, second, "conv-b", "second")
	writeTranscript(t, second, "conv-a", "duplicate")

	roots := testClientRoots(home)
	roots.AGYBrains = []string{first, second}
	output := filepath.Join(root, "out")
	report, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: home, OutputDir: output, Roots: roots})
	if err != nil {
		t.Fatalf("BundleWorkstation: %v", err)
	}
	got := strings.Join(bundledPaths(report), "\n")
	for _, want := range []string{"agent-transcripts/conv-a.transcript.jsonl", "agent-transcripts/conv-b.transcript.jsonl"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	winner, err := os.ReadFile(filepath.Join(output, "agent-transcripts", "conv-a.transcript.jsonl"))
	if err != nil || string(winner) != "first" {
		t.Fatalf("the first brain root must win a duplicate conversation: %q, %v", winner, err)
	}
	notes := strings.Join(report.Skipped, "\n")
	if !strings.Contains(notes, "2 Antigravity brain directories exist") || !strings.Contains(notes, "already bundled") {
		t.Fatalf("two brain roots and the duplicate must be reported, got: %s", notes)
	}
}

func TestBundleSkipsLocationsThatDoNotApply(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	mustWriteFile(t, filepath.Join(home, ".gemini", "config", "hooks.json"), "{}")
	// An empty root must never resolve against the working directory.
	t.Chdir(root)
	mustWriteFile(t, filepath.Join(root, "hooks.json"), "{}")
	mustWriteFile(t, filepath.Join(root, "skills", "stray", "SKILL.md"), "# stray")

	report, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: home, OutputDir: filepath.Join(root, "out"), IncludeShellHistory: true})
	if err != nil {
		t.Fatalf("BundleWorkstation: %v", err)
	}
	if len(report.Records) != 0 {
		t.Fatalf("empty roots must bundle nothing, got %v", bundledPaths(report))
	}
}

func TestBundleBoundsBrainRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	brains := make([]string, 0, MaxBrainRoots+2)
	for i := range MaxBrainRoots + 1 {
		brain := filepath.Join(home, fmt.Sprintf("brain-%d", i))
		writeTranscript(t, brain, fmt.Sprintf("conv-%d", i), "x")
		brains = append(brains, brain)
	}
	brains = append(brains, "")

	report, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: home, OutputDir: filepath.Join(root, "out"), Roots: ClientRoots{AGYBrains: brains}})
	if err != nil {
		t.Fatalf("BundleWorkstation: %v", err)
	}
	if len(report.Records) != MaxBrainRoots {
		t.Fatalf("expected %d transcripts, got %v", MaxBrainRoots, bundledPaths(report))
	}
	if notes := strings.Join(report.Skipped, "\n"); !strings.Contains(notes, "limit 8 reached") {
		t.Fatalf("the brain root bound must be reported, got: %s", notes)
	}
}

func TestBundleReadsPerOSFileLocations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	roots := testClientRoots(home)
	mustWriteFile(t, roots.ClaudeDesktopConfig, "{}")
	mustWriteFile(t, roots.PowerShellHistory, "Get-Date")

	report, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: home, OutputDir: filepath.Join(root, "out"), Roots: roots, IncludeShellHistory: true})
	if err != nil {
		t.Fatalf("BundleWorkstation: %v", err)
	}
	got := strings.Join(bundledPaths(report), "\n")
	for _, want := range []string{"agent-configs/claude/claude_desktop_config.json", "cli-logs/ConsoleHost_history.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
