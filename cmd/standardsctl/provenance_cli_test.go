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

// releaseDist writes release files beside a checksums.txt listing them and returns the
// manifest path and each file's SHA-256 by name.
func releaseDist(t *testing.T, files map[string]string, order []string) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	digests := make(map[string]string, len(files))
	var manifest strings.Builder
	for _, name := range order {
		writeFixtureFile(t, dir, name, files[name])
		sum := sha256.Sum256([]byte(files[name]))
		digests[name] = hex.EncodeToString(sum[:])
		manifest.WriteString(digests[name] + "  " + name + "\n")
	}
	return writeFixtureFile(t, dir, "checksums.txt", manifest.String()), digests
}

// Positive: -checksums turns every line of the release's checksums.txt into a subject whose
// digest is computed from the file beside it, and records the builder the release workflow
// passes, so the release flow signs one statement that covers every archive.
func TestRunProvenance_Positive_ChecksumsCoverEveryArchive(t *testing.T) {
	order := []string{"standards_1.0.0_linux_amd64.tar.gz", "standards_1.0.0_windows_amd64.zip"}
	manifest, digests := releaseDist(t, map[string]string{order[0]: "linux archive\n", order[1]: "windows archive\n"}, order)
	out := filepath.Join(t.TempDir(), "provenance.intoto.json")
	builder := "https://github.com/cordanaLLM/praetor/.github/workflows/release-binaries.yml@refs/tags/v1.0.0"
	stdout, err := captureStdout(t, func() error {
		return runProvenance([]string{"-checksums", manifest, "-builder", builder, "-out", out})
	})
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if !strings.Contains(stdout, "2 subjects") {
		t.Errorf("summary does not count the subjects: %q", stdout)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read statement: %v", err)
	}
	var stmt supplychain.SLSAStatement
	if err := json.Unmarshal(data, &stmt); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	if len(stmt.Subject) != 2 || stmt.Subject[1].Name != order[1] || stmt.Subject[1].Digest["sha256"] != digests[order[1]] {
		t.Fatalf("subjects = %+v", stmt.Subject)
	}
	if stmt.Predicate.RunDetails.Builder.ID != builder || stmt.Emission.Signed {
		t.Fatalf("builder = %q, emission = %+v", stmt.Predicate.RunDetails.Builder.ID, stmt.Emission)
	}
}

// Negative: -checksums beside a single-artifact flag, no subject source, a missing
// manifest, a malformed one and a line whose file no longer matches it all fail before any
// statement is written.
func TestRunProvenance_Negative_ChecksumsRefusals(t *testing.T) {
	order := []string{"a.tar.gz"}
	good, digests := releaseDist(t, map[string]string{"a.tar.gz": "archive\n"}, order)
	stale := writeFixtureFile(t, filepath.Dir(good), "stale.txt", strings.Repeat("0", 64)+"  a.tar.gz\n")
	broken := writeFixtureFile(t, filepath.Dir(good), "broken.txt", digests["a.tar.gz"]+" a.tar.gz\n")
	cases := map[string]struct {
		args []string
		want string
	}{
		"checksums and digest": {[]string{"-checksums", good, "-digest", digests["a.tar.gz"]}, "excludes -file, -artifact and -digest"},
		"checksums and file":   {[]string{"-checksums", good, "-file", good}, "excludes -file, -artifact and -digest"},
		"no source":            {nil, "-file is required"},
		"missing manifest":     {[]string{"-checksums", filepath.Join(filepath.Dir(good), "absent.txt")}, "read checksum manifest"},
		"malformed manifest":   {[]string{"-checksums", broken}, "checksum line 1"},
		"stale line":           {[]string{"-checksums", stale}, digests["a.tar.gz"]},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "provenance.json")
			_, err := captureStdout(t, func() error { return runProvenance(append(tc.args, "-out", out)) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatalf("a statement was written despite the error: %v", statErr)
			}
		})
	}
}
