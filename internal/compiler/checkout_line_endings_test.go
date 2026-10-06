package compiler

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows checkout writes AGENTS.md, the vendor files and the persona copies with CRLF under
// "* text=auto" (core.eol=native), so compile-context --verify reported an unmodified adopter
// out of sync (#781). These tests write the CRLF bytes directly, so they run alike on every
// platform.

// crlfCompiledFixture is a compiled repository with one persona and two plugin skills, every
// file then rewritten as a core.eol=crlf checkout writes it.
func crlfCompiledFixture(t *testing.T) string {
	t.Helper()
	root := skillFixture(t)
	writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "helper.md"), "---\nname: helper\n---\n# Helper\n\nBlock merges without receipts.\n")
	if err := compileFixture(t, root); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := verifyFixtureContext(t, root); err != nil {
		t.Fatalf("the LF checkout must verify before conversion: %v", err)
	}
	converted := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		converted++
		return os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "\n", "\r\n")), 0o600)
	})
	if err != nil || converted < 8 {
		t.Fatalf("convert the checkout to CRLF: %d files, %v", converted, err)
	}
	if text := readFixtureText(t, filepath.Join(root, "CLAUDE.md")); !strings.Contains(text, "\r\n") {
		t.Fatalf("fixture: CLAUDE.md was not converted: %q", text)
	}
	return root
}

func verifyFixtureContext(t *testing.T, root string) error {
	t.Helper()
	return VerifyCompiledContext(t.Context(), io.Discard, NewTranspiler(), filepath.Join(root, "AGENTS.md"), root)
}

// Positive: a uniformly CRLF checkout of an unmodified compiled repository verifies: the vendor
// files, the persona copies and the plugin skills.
func TestCheckoutLineEndings_Positive_CRLFCheckoutVerifies(t *testing.T) {
	root := crlfCompiledFixture(t)
	if err := verifyFixtureContext(t, root); err != nil {
		t.Fatalf("a CRLF checkout of unmodified context must verify: %v", err)
	}
	if verified, err := VerifyAgentProjections(t.Context(), root); err != nil || verified == 0 {
		t.Fatalf("persona projections of a CRLF checkout: %d, %v", verified, err)
	}
}

// Positive: a CRLF AGENTS.md compiles to the LF projections its LF form compiles to, never to a
// mix of the LF header and CRLF body lines.
func TestCheckoutLineEndings_Positive_CRLFSourceCompilesAsLF(t *testing.T) {
	const source = "# Policy\n\n## Rules\n\nKeep receipts.\n"
	lf, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	crlf, err := NewTranspiler().CompileContent(strings.ReplaceAll(source, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range lf.Files {
		if crlf.Files[i].Content != lf.Files[i].Content || strings.Contains(crlf.Files[i].Content, "\r") {
			t.Fatalf("%s compiled from CRLF differs from its LF form:\n%q\n%q", lf.Files[i].RelativePath, crlf.Files[i].Content, lf.Files[i].Content)
		}
	}
}

// Negative: a real content change in a CRLF checkout still fails, in a vendor file and in a
// persona copy, without a byte-for-byte note since the line endings were folded.
func TestCheckoutLineEndings_Negative_CRLFContentEditStillFails(t *testing.T) {
	root := crlfCompiledFixture(t)
	claude := filepath.Join(root, "CLAUDE.md")
	writeOutputFixture(t, claude, strings.Replace(readFixtureText(t, claude), "# Policy", "# Edited policy", 1))
	err := verifyFixtureContext(t, root)
	if err == nil || !strings.Contains(err.Error(), "target CLAUDE.md is out of sync") || strings.Contains(err.Error(), "byte for byte") {
		t.Fatalf("an edited CRLF vendor file: %v", err)
	}
	root = crlfCompiledFixture(t)
	writeOutputFixture(t, filepath.Join(root, ".claude", "agents", "helper.md"), "---\r\nname: helper\r\n---\r\n# Helper\r\n\r\nMerges may skip receipts.\r\n")
	if _, err := VerifyAgentProjections(t.Context(), root); !errors.Is(err, ErrAgentProjectionDrift) || strings.Contains(err.Error(), "byte for byte") {
		t.Fatalf("an edited CRLF persona copy: %v", err)
	}
}

// Boundary: a vendor file or a persona copy with mixed line endings is compared byte for byte,
// so it fails against its uniform source and the report says why.
func TestCheckoutLineEndings_Boundary_MixedEndingsStayByteExact(t *testing.T) {
	root := crlfCompiledFixture(t)
	claude := filepath.Join(root, "CLAUDE.md")
	writeOutputFixture(t, claude, strings.Replace(readFixtureText(t, claude), "\r\n", "\n", 1))
	err := verifyFixtureContext(t, root)
	if err == nil || !strings.Contains(err.Error(), "target CLAUDE.md is out of sync") ||
		!strings.Contains(err.Error(), "compared byte for byte") || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("a mixed-ending vendor file: %v", err)
	}
	root = crlfCompiledFixture(t)
	persona := filepath.Join(root, ".claude", "agents", "helper.md")
	writeOutputFixture(t, persona, strings.Replace(readFixtureText(t, persona), "\r\n", "\n", 1))
	_, err = VerifyAgentProjections(t.Context(), root)
	if !errors.Is(err, ErrAgentProjectionDrift) || !strings.Contains(err.Error(), "compared byte for byte") {
		t.Fatalf("a mixed-ending persona copy: %v", err)
	}
}
