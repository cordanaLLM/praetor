package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
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
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
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

func TestIsPriorRendering(t *testing.T) {
	text := []byte("version: 1\n")
	sum := sha256.Sum256(text)
	digests := map[string]string{hex.EncodeToString(sum[:]): "fixture"}
	if !isPriorRendering(text, digests) {
		t.Error("positive: the exact bytes of a recorded text must be recognised")
	}
	if isPriorRendering(append(text, '\n'), digests) {
		t.Error("negative: one byte more is an edited copy, not a prior rendering")
	}
	if isPriorRendering([]byte("version: 1\r\n"), digests) {
		t.Error("negative: a CRLF copy is not the recorded LF bytes")
	}
	if isPriorRendering(nil, digests) || isPriorRendering(text, nil) {
		t.Error("boundary: empty data and an empty digest set match nothing")
	}
}
