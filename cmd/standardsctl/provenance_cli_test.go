package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// provenanceArtifact writes an artifact file and returns its path and SHA-256.
func provenanceArtifact(t *testing.T, content string) (path, digest string) {
	t.Helper()
	path = writeFixtureFile(t, t.TempDir(), "dist/praetorctl", content)
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestRunProvenance_Positive_SubjectDigestFromFile(t *testing.T) {
	artifact, want := provenanceArtifact(t, "built binary\n")

	stdout, err := captureStdout(t, func() error { return runProvenance([]string{"-file", artifact}) })
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	var stmt supplychain.SLSAStatement
	if err := json.Unmarshal([]byte(stdout), &stmt); err != nil {
		t.Fatalf("stdout is not a statement: %v\n%s", err, stdout)
	}
	if stmt.Subject[0].Digest["sha256"] != want || stmt.Subject[0].Name != "praetorctl" {
		t.Errorf("subject = %+v, want praetorctl sha256 %s", stmt.Subject, want)
	}
	if stmt.Emission.Signed {
		t.Error("statement claims to be signed")
	}

	out := filepath.Join(t.TempDir(), "provenance.json")
	stdout, err = captureStdout(t, func() error {
		return runProvenance([]string{"-file", artifact, "-digest", want, "-artifact", "praetorctl-v1", "-out", out})
	})
	if err != nil {
		t.Fatalf("provenance with matching -digest: %v", err)
	}
	if !strings.Contains(stdout, "Unsigned SLSA v1.0 provenance statement") || !strings.Contains(stdout, want) {
		t.Errorf("summary does not name the statement unsigned with its digest: %q", stdout)
	}
	data, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(data), `"praetorEmission"`) || !strings.Contains(string(data), `"praetorctl-v1"`) {
		t.Errorf("written statement lacks the unsigned marker or subject name: %v\n%s", err, data)
	}
}

func TestRunProvenance_Negative_DigestAloneOrMismatchRefused(t *testing.T) {
	artifact, want := provenanceArtifact(t, "built binary\n")
	_, stale := provenanceArtifact(t, "an older build\n")

	if err := runProvenance([]string{"-digest", want}); err == nil || !strings.Contains(err.Error(), "-file is required") {
		t.Errorf("-digest without -file must be refused, got %v", err)
	}
	out := filepath.Join(t.TempDir(), "provenance.json")
	err := runProvenance([]string{"-file", artifact, "-digest", stale, "-out", out})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("mismatching -digest must be refused naming the computed digest, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("a refused run wrote %s (stat: %v)", out, statErr)
	}
}

func TestRunProvenance_Boundary_EmptyArtifactRefused(t *testing.T) {
	artifact, _ := provenanceArtifact(t, "")
	if err := runProvenance([]string{"-file", artifact}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("zero-byte artifact must be refused, got %v", err)
	}
}
