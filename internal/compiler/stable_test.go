package compiler

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const layeredSource = "# Policy\n\n<!-- praetor:head -->\n\nStatic rule.\n\n<!-- praetor:config -->\n\nConfig text.\n\n<!-- praetor:tail -->\n\nCommands.\n"

func stableFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, path, source)
	return root, path
}

func runStable(t *testing.T, source string) (string, error) {
	t.Helper()
	_, path := stableFixture(t, source)
	var out bytes.Buffer
	err := VerifyStableContext(context.Background(), &out, NewTranspiler(), path)
	return out.String(), err
}

func TestVerifyStableContext_Positive_LayeredSourcePasses(t *testing.T) {
	out, err := runStable(t, layeredSource)
	if err != nil || out != "" {
		t.Fatalf("layered source: err=%v out=%q", err, out)
	}
}

// Negative (rule 13): a timestamp planted in the head band is refused; the same text in the
// tail band is not the head's concern.
func TestVerifyStableContext_Negative_PlantedTimestampInHeadRefused(t *testing.T) {
	planted := strings.Replace(layeredSource, "Static rule.", "Static rule, built 2026-10-07T12:30:00Z.", 1)
	_, err := runStable(t, planted)
	if err == nil || !strings.Contains(err.Error(), "timestamp") || !strings.Contains(err.Error(), "head line") {
		t.Fatalf("planted timestamp accepted: %v", err)
	}
	inTail := strings.Replace(layeredSource, "Commands.", "Commands, built 2026-10-07T12:30:00Z.", 1)
	if _, err := runStable(t, inTail); err != nil {
		t.Fatalf("tail text refused: %v", err)
	}
}

func TestVerifyStableContext_Negative_PlantedDigestPathAndCounterRefused(t *testing.T) {
	for _, text := range []string{"pin sha256:0123456789abcdef0123", "dir /home/someone/work", "run #7"} {
		planted := strings.Replace(layeredSource, "Static rule.", text, 1)
		if _, err := runStable(t, planted); err == nil {
			t.Fatalf("%q accepted in the head", text)
		}
	}
}

// Boundary: an unmarked source passes with the warning that names the markers to add.
func TestVerifyStableContext_Boundary_UnmarkedSourceWarns(t *testing.T) {
	out, err := runStable(t, "# Policy\n\nStatic rule.\n")
	if err != nil {
		t.Fatalf("unmarked source failed: %v", err)
	}
	if !strings.Contains(out, UnlayeredWarning) || !strings.Contains(out, "<!-- praetor:head -->") {
		t.Fatalf("warning missing: %q", out)
	}
}

func TestVerifyStableContext_Negative_MissingSourceFails(t *testing.T) {
	root := t.TempDir()
	err := VerifyStableContext(context.Background(), &bytes.Buffer{}, NewTranspiler(), filepath.Join(root, "AGENTS.md"))
	if err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestVerifyRenderTwice_Positive_BothRendersMatch(t *testing.T) {
	if err := verifyRenderTwice(NewTranspiler(), layeredSource); err != nil {
		t.Fatal(err)
	}
	if err := verifyRenderTwice(NewTranspiler(), ""); err == nil {
		t.Fatal("empty source rendered")
	}
}

// Negative: the comparison names a file whose bytes differ between the two renders.
func TestDriftedFiles_Negative_NamesDifferingFile(t *testing.T) {
	a, err := NewTranspiler().CompileContent(layeredSource)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTranspiler().CompileContent(layeredSource)
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := driftedFiles(a, b); err != nil || len(drift) != 0 {
		t.Fatalf("identical renders drift: %v %v", drift, err)
	}
	b.Files[3].Content += " 2026"
	drift, err := driftedFiles(a, b)
	if err != nil || len(drift) != 1 || drift[0] != b.Files[3].RelativePath {
		t.Fatalf("drift = %v, %v", drift, err)
	}
	b.Files = b.Files[:2]
	if _, err := driftedFiles(a, b); err == nil {
		t.Fatal("different file counts accepted")
	}
}

func TestVerifyStableContext_Positive_RepositoryAgentsMdIsStable(t *testing.T) {
	var out bytes.Buffer
	path := filepath.Join("..", "..", "AGENTS.md")
	if err := VerifyStableContext(context.Background(), &out, NewTranspiler(), path); err != nil || out.Len() != 0 {
		t.Fatalf("repository AGENTS.md: err=%v out=%q", err, out.String())
	}
}
