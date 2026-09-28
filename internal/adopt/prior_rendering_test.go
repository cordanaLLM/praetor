package adopt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// readFixtureDir returns every file directly under dir, keyed by its name.
func readFixtureDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[entry.Name()] = data
	}
	return fixtures
}

// assertPriorDigestsReproduced replays a set of earlier Praetor texts in both directions: every
// fixture under dir is a recognised digest and every digest has a fixture, so the set can
// neither claim bytes nobody can reproduce nor silently stop covering a fixture.
func assertPriorDigestsReproduced(t *testing.T, dir string, digests map[string]string) {
	t.Helper()
	fixtures := readFixtureDir(t, dir)
	seen := make(map[string]bool, len(fixtures))
	for name, data := range fixtures {
		digest := fixtureDigest(t, name, data)
		if _, ok := digests[digest]; !ok {
			t.Errorf("fixture %s (%s) is not a recognised prior rendering", name, digest)
		}
		seen[digest] = true
	}
	for digest, origin := range digests {
		if !seen[digest] {
			t.Errorf("digest %s (%s) has no fixture under %s", digest, origin, dir)
		}
	}
}

// fixtureDigest is the key priorRendering looks data up by.
func fixtureDigest(t *testing.T, name string, data []byte) string {
	t.Helper()
	digest, _, err := util.CanonicalTextDigest(data)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return digest
}

// priorDigestSet records text the way the earlier-text sets do.
func priorDigestSet(t *testing.T, text string) map[string]string {
	t.Helper()
	return map[string]string{fixtureDigest(t, "recorded text", []byte(text)): "fixture"}
}

// Positive: the recorded LF text and its CRLF checkout (core.autocrlf on Windows) are both
// recognised, and the CRLF one is reported as such so a refresh can keep its style (HISS-21).
func TestIsPriorRendering_Positive_EitherConsistentLineEndingStyle(t *testing.T) {
	const text = "version: 1\nname: x\n"
	digests := priorDigestSet(t, text)
	if known, crlf := priorRendering([]byte(text), digests); !known || crlf {
		t.Errorf("the recorded LF text: known=%v crlf=%v", known, crlf)
	}
	if known, crlf := priorRendering([]byte(crlfText(text)), digests); !known || !crlf {
		t.Errorf("the CRLF checkout of the recorded text: known=%v crlf=%v", known, crlf)
	}
	if !isPriorRendering([]byte(crlfText(text)), digests) {
		t.Error("isPriorRendering must apply the same rule as priorRendering")
	}
}

// Negative: an edited copy is not a prior rendering, in either line-ending style.
func TestIsPriorRendering_Negative_EditedCopies(t *testing.T) {
	const text = "version: 1\n"
	digests := priorDigestSet(t, text)
	for name, edited := range map[string]string{
		"LF, one line more":   text + "\n",
		"CRLF, one line more": "version: 1\r\n\r\n",
		"CRLF, value changed": "version: 2\r\n",
	} {
		if isPriorRendering([]byte(edited), digests) {
			t.Errorf("%s: an edited copy was recognised", name)
		}
	}
}

// Boundary: mixed line endings and lone carriage returns match nothing, even when the LF text
// underneath is the recorded one, and empty data or an empty digest set match nothing.
func TestIsPriorRendering_Boundary_MixedEndingsAndEmptyInputs(t *testing.T) {
	const text = "version: 1\nname: x\n"
	digests := priorDigestSet(t, text)
	for _, mixed := range []string{"version: 1\r\nname: x\n", "version: 1\rname: x\n"} {
		if known, _ := priorRendering([]byte(mixed), digests); known {
			t.Errorf("%q: mixed line endings were recognised", mixed)
		}
	}
	if isPriorRendering(nil, digests) || isPriorRendering([]byte(text), nil) {
		t.Error("empty data and an empty digest set match nothing")
	}
}
