package supplychain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	digestA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	digestB = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
)

// Positive: a GoReleaser checksums.txt becomes one subject per line, in manifest order.
func TestParseChecksumsBuildsOneSubjectPerLine(t *testing.T) {
	manifest := digestA + "  standards_1.0.0_linux_amd64.tar.gz\n" +
		digestB + "  standards_1.0.0_linux_amd64.tar.gz.cyclonedx.json\n"
	subjects, err := ParseChecksums([]byte(manifest))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if len(subjects) != 2 || subjects[0].Name != "standards_1.0.0_linux_amd64.tar.gz" || subjects[1].Digest["sha256"] != digestB {
		t.Fatalf("subjects = %+v", subjects)
	}
}

// releaseManifest writes each file into a fresh directory beside a checksums.txt listing
// it with the digest of its bytes, and returns the manifest path and the digests by name.
func releaseManifest(t *testing.T, names []string) (string, map[string]string) {
	t.Helper()
	return releaseManifestOf(t, names, func(name string) []byte { return []byte("bytes of " + name) })
}

// releaseManifestOf is releaseManifest with each file's bytes taken from content.
func releaseManifestOf(t *testing.T, names []string, content func(name string) []byte) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	digests := make(map[string]string, len(names))
	var manifest strings.Builder
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content(name), 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content(name))
		digests[name] = hex.EncodeToString(sum[:])
		manifest.WriteString(digests[name] + "  " + name + "\n")
	}
	path := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(path, []byte(manifest.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, digests
}

// Positive: the statement built from a manifest names every listed file, with the digest
// computed from the file beside the manifest, and encodes as a strict in-toto v1 Statement.
func TestGenerateSLSAProvenanceFromChecksumsNamesEveryFile(t *testing.T) {
	names := []string{"standards_1.0.0_linux_amd64.tar.gz", "sboms/standards_1.0.0_linux_amd64.tar.gz.cyclonedx.json"}
	manifest, digests := releaseManifest(t, names)
	stmt, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "https://github.com/acme/tool/.github/workflows/release.yml@refs/tags/v1.0.0"})
	if err != nil {
		t.Fatalf("GenerateSLSAProvenanceFromChecksums: %v", err)
	}
	if len(stmt.Subject) != 2 || stmt.PredicateType != "https://slsa.dev/provenance/v1" {
		t.Fatalf("statement = %+v", stmt)
	}
	for i, name := range names {
		if stmt.Subject[i].Name != name || stmt.Subject[i].Digest["sha256"] != digests[name] {
			t.Errorf("subject %d = %+v, want %s sha256 %s", i, stmt.Subject[i], name, digests[name])
		}
	}
	data, err := json.Marshal(stmt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := CheckInTotoStatement(data); err != nil {
		t.Errorf("manifest statement fails the strict in-toto check: %v", err)
	}
}

// Negative: a manifest line is never taken on trust. A line whose digest no longer matches
// its file, a listed file that is missing, and a name that climbs out of the manifest's
// directory each refuse the whole statement.
func TestGenerateSLSAProvenanceFromChecksumsRefusesUntrustedLines(t *testing.T) {
	manifest, digests := releaseManifest(t, []string{"a.tar.gz"})
	dir := filepath.Dir(manifest)
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := map[string]struct{ manifest, want string }{
		"stale digest":   {write("stale.txt", digestA+"  a.tar.gz\n"), digests["a.tar.gz"]},
		"missing file":   {write("missing.txt", digestA+"  absent.tar.gz\n"), "checksum line 1"},
		"parent escape":  {write("escape.txt", digestA+"  ../a.tar.gz\n"), "not a path inside"},
		"absolute path":  {write("absolute.txt", digestA+"  /etc/passwd\n"), "not a path inside"},
		"no manifest":    {filepath.Join(dir, "absent.txt"), "read checksum manifest"},
		"empty manifest": {write("empty.txt", ""), "holds no lines"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: tc.manifest, BuilderID: "b"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{BuilderID: "b"}); err == nil {
		t.Error("request without a manifest path accepted")
	}
}

// Negative: every malformed manifest fails whole, so no archive is silently left out of
// the provenance.
func TestParseChecksumsRejectsMalformedManifests(t *testing.T) {
	cases := map[string]string{
		"empty manifest":         "",
		"whitespace only":        " \n",
		"single space separator": digestA + " a.tar.gz\n",
		"tab separator":          digestA + "\ta.tar.gz\n",
		"uppercase digest":       strings.ToUpper(digestA) + "  a.tar.gz\n",
		"short digest":           digestA[:63] + "  a.tar.gz\n",
		"missing name":           digestA + "  \n",
		"blank line inside":      digestA + "  a.tar.gz\n\n" + digestB + "  b.tar.gz\n",
		"duplicate name":         digestA + "  a.tar.gz\n" + digestB + "  a.tar.gz\n",
		"sha512 digest":          strings.Repeat("a", 128) + "  a.tar.gz\n",
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			if subjects, err := ParseChecksums([]byte(manifest)); err == nil {
				t.Fatalf("manifest %q accepted as %+v", manifest, subjects)
			}
		})
	}
}

// Boundary: binary-mode lines, CRLF endings and a missing final newline are all the same
// manifest; exactly the subject limit is accepted and one more line is refused.
func TestParseChecksumsBoundaries(t *testing.T) {
	for name, manifest := range map[string]string{
		"binary mode":         digestA + " *a.tar.gz\n",
		"crlf endings":        digestA + "  a.tar.gz\r\n",
		"no trailing newline": digestA + "  a.tar.gz",
	} {
		subjects, err := ParseChecksums([]byte(manifest))
		if err != nil || len(subjects) != 1 || subjects[0].Name != "a.tar.gz" {
			t.Errorf("%s: subjects %+v, err %v", name, subjects, err)
		}
	}
	build := func(lines int) []byte {
		var b strings.Builder
		for i := 0; i < lines && i <= maxProvenanceSubjects; i++ {
			fmt.Fprintf(&b, "%s  archive-%d.tar.gz\n", digestA, i)
		}
		return []byte(b.String())
	}
	if subjects, err := ParseChecksums(build(maxProvenanceSubjects)); err != nil || len(subjects) != maxProvenanceSubjects {
		t.Fatalf("%d lines: %d subjects, err %v", maxProvenanceSubjects, len(subjects), err)
	}
	if _, err := ParseChecksums(build(maxProvenanceSubjects + 1)); err == nil {
		t.Fatalf("%d lines accepted", maxProvenanceSubjects+1)
	}
}

// Negative and boundary: the subject list itself is validated, and the per-file byte bound
// and context apply to every listed file.
func TestChecksumSubjectsValidation(t *testing.T) {
	one := func(name, digest string) Subject {
		return Subject{Name: name, Digest: map[string]string{"sha256": digest}}
	}
	cases := map[string][]Subject{
		"no subjects":     nil,
		"unnamed subject": {one(" ", digestA)},
		"duplicate names": {one("a", digestA), one("a", digestB)},
		"missing sha256":  {{Name: "a", Digest: map[string]string{"sha512": digestA}}},
	}
	for name, subjects := range cases {
		if err := validateSubjects(subjects); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := validateSubjects([]Subject{one("a", digestA)}); err != nil {
		t.Errorf("single valid subject refused: %v", err)
	}
	manifest, _ := releaseManifest(t, []string{"a.tar.gz"})
	size := int64(len("bytes of a.tar.gz"))
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b", MaxBytes: size}); err != nil {
		t.Errorf("file of exactly the byte bound refused: %v", err)
	}
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b", MaxBytes: size - 1}); err == nil {
		t.Error("file one byte over the bound accepted")
	}
	var absent context.Context
	if _, _, err := GenerateSLSAProvenanceFromChecksums(absent, ChecksumsRequest{ManifestPath: manifest, BuilderID: "b"}); err == nil {
		t.Error("nil context accepted")
	}
}
