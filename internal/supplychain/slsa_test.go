package supplychain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeArtifact writes content to a fresh temporary file and returns its path and the
// SHA-256 of its bytes, the value every statement must attest.
func writeArtifact(t *testing.T, name string, content []byte) (path, digest string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:])
}

func TestGenerateSLSAProvenance_Positive_DigestFromArtifactBytes(t *testing.T) {
	path, want := writeArtifact(t, "praetorctl-linux-amd64", []byte("release binary bytes\n"))

	stmt, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "ghcr.io/cordanallm/builder"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(stmt.Subject) != 1 || stmt.Subject[0].Digest["sha256"] != want {
		t.Fatalf("subject digest is not the artifact's SHA-256 %s: %+v", want, stmt.Subject)
	}
	if stmt.Subject[0].Name != "praetorctl-linux-amd64" {
		t.Errorf("subject name should default to the file's base name, got %q", stmt.Subject[0].Name)
	}
	if stmt.Emission != UnsignedEmission() || stmt.Emission.Signed || stmt.Emission.Notice == "" {
		t.Errorf("statement must carry the unsigned emission marker, got %+v", stmt.Emission)
	}

	named, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, ArtifactName: "praetorctl", BuilderID: "b", ExpectedSHA256: want})
	if err != nil {
		t.Fatalf("matching expected digest refused: %v", err)
	}
	if named.Subject[0].Name != "praetorctl" || named.Subject[0].Digest["sha256"] != want {
		t.Errorf("explicit name or matching digest not honored: %+v", named.Subject)
	}
}

func TestGenerateSLSAProvenance_Positive_JSONMarksStatementUnsigned(t *testing.T) {
	path, _ := writeArtifact(t, "artifact.tar.gz", []byte{0x1f, 0x8b, 0x08})
	stmt, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	data, err := json.Marshal(stmt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var emission Emission
	if err := json.Unmarshal(decoded["praetorEmission"], &emission); err != nil {
		t.Fatalf("praetorEmission missing or malformed in %s: %v", data, err)
	}
	if emission.Signed || emission.SubjectDigest != "computed-from-artifact-bytes" || !strings.Contains(emission.Notice, "unsigned") {
		t.Errorf("JSON does not mark the statement unsigned: %+v", emission)
	}
	if string(decoded["_type"]) != `"https://in-toto.io/Statement/v1"` {
		t.Errorf("in-toto statement type changed: %s", decoded["_type"])
	}
}

func TestGenerateSLSAProvenance_Negative_DigestNotTakenOnTrust(t *testing.T) {
	path, actual := writeArtifact(t, "artifact", []byte("real bytes"))
	_, other := writeArtifact(t, "other", []byte("different bytes"))

	if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{BuilderID: "b", ExpectedSHA256: actual}); err == nil {
		t.Error("digest-only request accepted: the subject digest would be caller-supplied")
	}
	_, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b", ExpectedSHA256: other})
	if err == nil || !strings.Contains(err.Error(), actual) {
		t.Errorf("mismatching expected digest must be refused naming the computed digest, got %v", err)
	}
	cases := map[string]string{
		"63 characters (one short)": actual[:len(actual)-1],
		"uppercase hex":             strings.ToUpper(actual),
		"non-hex characters":        strings.Repeat("g", 64),
	}
	for name, digest := range cases {
		if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b", ExpectedSHA256: digest}); err == nil {
			t.Errorf("%s: malformed expected digest %q accepted", name, digest)
		}
	}
}

func TestGenerateSLSAProvenance_Negative_UnreadableArtifacts(t *testing.T) {
	dir := t.TempDir()
	target, _ := writeArtifact(t, "target", []byte("bytes"))
	for name, path := range map[string]string{"missing file": filepath.Join(dir, "missing"), "directory": dir} {
		if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b"}); err == nil {
			t.Errorf("%s accepted as an artifact", name)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this platform or account: %v", err)
	}
	if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: link, BuilderID: "b"}); err == nil {
		t.Error("symlinked artifact accepted")
	}
}

func TestGenerateSLSAProvenance_Boundary_ArtifactSize(t *testing.T) {
	empty, _ := writeArtifact(t, "empty", nil)
	if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: empty, BuilderID: "b"}); err == nil {
		t.Error("zero-byte artifact accepted")
	}
	one, oneDigest := writeArtifact(t, "one", []byte{0})
	if stmt, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: one, BuilderID: "b"}); err != nil || stmt.Subject[0].Digest["sha256"] != oneDigest {
		t.Errorf("one-byte artifact: %v", err)
	}

	const limit = 4096
	exact, exactDigest := writeArtifact(t, "exact", bytes.Repeat([]byte{0xa5}, limit))
	if stmt, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: exact, BuilderID: "b", MaxBytes: limit}); err != nil || stmt.Subject[0].Digest["sha256"] != exactDigest {
		t.Errorf("artifact of exactly the byte bound: %v", err)
	}
	over, _ := writeArtifact(t, "over", bytes.Repeat([]byte{0xa5}, limit+1))
	if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: over, BuilderID: "b", MaxBytes: limit}); err == nil {
		t.Error("artifact one byte over the bound accepted")
	}
	for _, bound := range []int64{-1, MaxArtifactBytes + 1} {
		if _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: one, BuilderID: "b", MaxBytes: bound}); err == nil {
			t.Errorf("byte bound %d accepted", bound)
		}
	}
}

func TestGenerateSLSAProvenance_Boundary_Context(t *testing.T) {
	path, _ := writeArtifact(t, "artifact", []byte("bytes"))
	var absent context.Context
	if _, err := GenerateSLSAProvenance(absent, ProvenanceRequest{ArtifactPath: path, BuilderID: "b"}); err == nil {
		t.Error("nil context accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := GenerateSLSAProvenance(cancelled, ProvenanceRequest{ArtifactPath: path, BuilderID: "b"}); err == nil {
		t.Error("cancelled context accepted")
	}
}
