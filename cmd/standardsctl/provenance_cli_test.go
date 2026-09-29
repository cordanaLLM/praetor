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
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_WORKFLOW_REF", "example.com/acme/praetor/.github/workflows/release.yml@refs/tags/v1.0.0")

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
	const wantBuilder = "https://github.com/example.com/acme/praetor/.github/workflows/release.yml@refs/tags/v1.0.0"
	if stmt.Predicate.RunDetails.Builder.ID != wantBuilder {
		t.Errorf("builder = %q, want %q", stmt.Predicate.RunDetails.Builder.ID, wantBuilder)
	}
	const wantBuildType = "https://cordanallm.github.io/praetor/slsa/build/v1"
	if stmt.Predicate.BuildDefinition.BuildType != wantBuildType {
		t.Errorf("buildType = %q, want %q", stmt.Predicate.BuildDefinition.BuildType, wantBuildType)
	}
	if err := supplychain.CheckInTotoStatement([]byte(stdout)); err != nil {
		t.Errorf("stdout is not a strict in-toto v1 statement: %v", err)
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
	if err != nil || !strings.Contains(string(data), `"praetorctl-v1"`) {
		t.Errorf("written statement lacks the subject name: %v\n%s", err, data)
	}
	if err := supplychain.CheckInTotoStatement(data); err != nil || strings.Contains(string(data), "praetorEmission") {
		t.Errorf("written statement is not a strict in-toto v1 statement cosign can verify: %v\n%s", err, data)
	}
}

func TestRunProvenance_Negative_DigestAloneOrMismatchRefused(t *testing.T) {
	artifact, want := provenanceArtifact(t, "built binary\n")
	_, stale := provenanceArtifact(t, "an older build\n")

	if err := runProvenance([]string{"-digest", want}); err == nil || !strings.Contains(err.Error(), "-file is required") {
		t.Errorf("-digest without -file must be refused, got %v", err)
	}
	out := filepath.Join(t.TempDir(), "provenance.json")
	err := runProvenance([]string{"-file", artifact, "-digest", stale, "-builder", "example.com/acme/builder", "-out", out})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("mismatching -digest must be refused naming the computed digest, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("a refused run wrote %s (stat: %v)", out, statErr)
	}
}

func TestRunProvenance_Boundary_EmptyArtifactRefused(t *testing.T) {
	artifact, _ := provenanceArtifact(t, "")
	if err := runProvenance([]string{"-file", artifact, "-builder", "example.com/acme/builder"}); err == nil || !strings.Contains(err.Error(), "empty") {
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
	if stmt.Predicate.RunDetails.Builder.ID != builder {
		t.Fatalf("builder = %q, want %q", stmt.Predicate.RunDetails.Builder.ID, builder)
	}
	if err := supplychain.CheckInTotoStatement(data); err != nil {
		t.Errorf("written statement is not a strict in-toto v1 statement: %v", err)
	}
}

// Negative: -checksums beside a single-artifact flag, no subject source, a missing
// manifest, a malformed one and a line whose file no longer matches it all fail before any
// statement is written.
func TestRunProvenance_Negative_ChecksumsRefusals(t *testing.T) {
	// The builder resolves from the workflow identity, so each refusal below is the manifest's.
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_WORKFLOW_REF", "example.com/acme/praetor/.github/workflows/release.yml@refs/tags/v1.0.0")
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

func TestRunProvenance_BuilderResolution_PNB(t *testing.T) {
	artifact, _ := provenanceArtifact(t, "binary content\n")

	// 1. builder env set -> derived
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_WORKFLOW_REF", "example.com/acme/repo/.github/workflows/build.yml@refs/heads/main")
	got, err := resolveBuilder("")
	if err != nil {
		t.Fatalf("resolveBuilder with env set: %v", err)
	}
	const wantDerived = "https://github.com/example.com/acme/repo/.github/workflows/build.yml@refs/heads/main"
	if got != wantDerived {
		t.Errorf("resolveBuilder() = %q, want %q", got, wantDerived)
	}

	// 2. unset+empty flag -> error
	t.Setenv("GITHUB_SERVER_URL", "")
	t.Setenv("GITHUB_WORKFLOW_REF", "")
	if _, err := resolveBuilder(""); err == nil || !strings.Contains(err.Error(), "-builder is required outside GitHub Actions") {
		t.Errorf("resolveBuilder with unset env and empty flag: want the required-flag error, got %v", err)
	}
	if err := runProvenance([]string{"-file", artifact}); err == nil || !strings.Contains(err.Error(), "-builder is required outside GitHub Actions") {
		t.Errorf("runProvenance with unset env and no flag: want error, got %v", err)
	}
	if err := runProvenance([]string{"-file", artifact, "-builder", ""}); err == nil || !strings.Contains(err.Error(), "-builder is required outside GitHub Actions") {
		t.Errorf("runProvenance with unset env and empty flag: want error, got %v", err)
	}

	// 3. flag wins
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_WORKFLOW_REF", "example.com/acme/repo/.github/workflows/build.yml@refs/heads/main")
	got, err = resolveBuilder("explicit-builder")
	if err != nil {
		t.Fatalf("resolveBuilder with explicit flag: %v", err)
	}
	if got != "explicit-builder" {
		t.Errorf("resolveBuilder flag wins: got %q, want explicit-builder", got)
	}

	// 4. whitespace flag -> error
	if _, err := resolveBuilder("   "); err == nil || !strings.Contains(err.Error(), "-builder") {
		t.Errorf("resolveBuilder with whitespace flag: want error, got %v", err)
	}
	if err := runProvenance([]string{"-file", artifact, "-builder", "   "}); err == nil || !strings.Contains(err.Error(), "-builder") {
		t.Errorf("runProvenance with whitespace flag: want error, got %v", err)
	}
}

// Negative: a file whose bytes are not the type its name declares is refused under the
// default content check, naming the file, the expected format and the opt-out, and an
// unknown -content-check value is refused before any file is read.
func TestRunProvenance_Negative_ContentMismatchRefused(t *testing.T) {
	deb := writeFixtureFile(t, t.TempDir(), "dist/linux-image-7.2.4_amd64.deb", "not a debian package\n")
	out := filepath.Join(t.TempDir(), "provenance.json")
	err := runProvenance([]string{"-file", deb, "-builder", "example.com/acme/builder", "-out", out})
	if err == nil || !strings.Contains(err.Error(), "linux-image-7.2.4_amd64.deb") ||
		!strings.Contains(err.Error(), "Debian binary package") || !strings.Contains(err.Error(), "-content-check=report") {
		t.Errorf("text file named .deb: got %v, want a refusal naming the file, the format and the opt-out", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("a refused run wrote %s (stat: %v)", out, statErr)
	}
	if err := runProvenance([]string{"-file", deb, "-builder", "b", "-content-check", "off"}); err == nil || !strings.Contains(err.Error(), "flag -content-check") {
		t.Errorf("unknown -content-check value: got %v", err)
	}
}

// Positive: -content-check=report attests the mismatching file and warns on stderr that its
// content is unverified, naming the expected format.
func TestRunProvenance_Positive_ContentReportAttestsUnverified(t *testing.T) {
	efi := writeFixtureFile(t, t.TempDir(), "dist/BOOTX64.EFI", "MZfake")
	out := filepath.Join(t.TempDir(), "provenance.json")
	stderr, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error {
			return runProvenance([]string{"-file", efi, "-builder", "b", "-out", out, "-content-check=report"})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("report mode refused: %v", err)
	}
	if !strings.Contains(stderr, "warning: content of BOOTX64.EFI is UNVERIFIED: expected an EFI image") {
		t.Errorf("stderr lacks the unverified warning: %q", stderr)
	}
	if data, readErr := os.ReadFile(out); readErr != nil || !strings.Contains(string(data), `"BOOTX64.EFI"`) {
		t.Errorf("report mode wrote no statement for the file: %v", readErr)
	}
}

// Boundary: a subject no content rule covers is named in one unchecked note, never passed
// off as verified, and a verified subject adds no line.
func TestReportContentVerdicts_Boundary(t *testing.T) {
	var b strings.Builder
	reportContentVerdicts(&b, []supplychain.ContentVerdict{
		{Name: "a.tar.gz", Status: supplychain.ContentUnchecked},
		{Name: "pkg.deb", Format: "a Debian binary package", Status: supplychain.ContentVerified},
		{Name: "b.spdx.json", Status: supplychain.ContentUnchecked},
	})
	want := "note: content unchecked for 2 subject(s) of a file type praetorctl has no content rule for: a.tar.gz, b.spdx.json\n"
	if b.String() != want {
		t.Errorf("report = %q, want %q", b.String(), want)
	}
	b.Reset()
	reportContentVerdicts(&b, nil)
	if b.Len() != 0 {
		t.Errorf("no verdicts printed %q", b.String())
	}
}
