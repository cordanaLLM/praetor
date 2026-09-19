package contextopt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// openTestRoot pins a fresh temporary directory for the creation tests.
func openTestRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := OpenDirectory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("pin directory: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	return root
}

func TestCreateRootSnapshotCreatesOnceAndKeepsAnExistingFile(t *testing.T) {
	root := openTestRoot(t)
	created, err := CreateRootSnapshot(t.Context(), root, "LEDGER.md", []byte("# Ledger\n"), 0o600)
	if err != nil || !created {
		t.Fatalf("first creation failed: created=%v, %v", created, err)
	}
	path := filepath.Join(root.Name(), "LEDGER.md")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "# Ledger\n" {
		t.Fatalf("published content is wrong: %q, %v", content, err)
	}

	// A second call finds the name taken: it reports nothing created and leaves the
	// file byte-for-byte alone rather than replacing it.
	created, err = CreateRootSnapshot(t.Context(), root, "LEDGER.md", []byte("# Replaced\n"), 0o600)
	if err != nil || created {
		t.Fatalf("second creation replaced an existing file: created=%v, %v", created, err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "# Ledger\n" {
		t.Fatalf("existing file was rewritten: %q, %v", content, err)
	}

	// No staging entry survives a completed call.
	entries, err := os.ReadDir(root.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".pending") {
			t.Fatalf("staging entry %s was left behind", entry.Name())
		}
	}
}

func TestCreateRootSnapshotRejectsInvalidArguments(t *testing.T) {
	root := openTestRoot(t)
	cases := map[string]struct {
		name string
		data []byte
		mode os.FileMode
	}{
		"nested name":  {"sub/LEDGER.md", []byte("x"), 0o600},
		"parent name":  {"../LEDGER.md", []byte("x"), 0o600},
		"dot name":     {".", []byte("x"), 0o600},
		"empty name":   {"", []byte("x"), 0o600},
		"zero mode":    {"LEDGER.md", []byte("x"), 0},
		"wide mode":    {"LEDGER.md", []byte("x"), 0o666},
		"binary bytes": {"LEDGER.md", []byte{0xff, 0x00}, 0o600},
		"oversize":     {"LEDGER.md", bytes.Repeat([]byte("a"), MaxSourceBytes+1), 0o600},
	}
	for label, test := range cases {
		created, err := CreateRootSnapshot(t.Context(), root, test.name, test.data, test.mode)
		if err == nil || created {
			t.Fatalf("%s accepted: created=%v, %v", label, created, err)
		}
	}
	if created, err := CreateRootSnapshot(t.Context(), nil, "LEDGER.md", []byte("x"), 0o600); err == nil || created {
		t.Fatalf("nil root accepted: %v, %v", created, err)
	}
	if entries, err := os.ReadDir(root.Name()); err != nil || len(entries) != 0 {
		t.Fatalf("a refused creation wrote something: %v, %v", entries, err)
	}
}

// TestCreateRootSnapshotBoundaryConcurrentCreatorsNeverPublishPartialContent pins the
// invariant the state bootstrap depends on. An exclusive create alone publishes the name
// before the content: a process that stats it between OpenFile and WriteString sees a
// zero-byte file, concludes the ledger is present, and the audit that runs next reads an
// empty ledger. Staging the bytes and linking them into place makes the name either
// absent or complete, never partial.
func TestCreateRootSnapshotBoundaryConcurrentCreatorsNeverPublishPartialContent(t *testing.T) {
	const name = "LEDGER.md"
	content := strings.Repeat("# Ledger row\n", 4096)
	root := openTestRoot(t)
	path := filepath.Join(root.Name(), name)

	// maxWatchRounds bounds the observer loop (HISS-02); it stops earlier on the signal.
	const maxWatchRounds = 1 << 16
	stop := make(chan struct{})
	shortSize := int64(-1)
	var watcher sync.WaitGroup
	watcher.Go(func() {
		for round := 0; round < maxWatchRounds; round++ {
			select {
			case <-stop:
				return
			default:
			}
			info, err := os.Stat(path)
			if err == nil && info.Size() != int64(len(content)) {
				shortSize = info.Size()
				return
			}
		}
	})

	var wg sync.WaitGroup
	results := make([]bool, 8)
	failures := make([]error, len(results))
	for i := range results {
		wg.Go(func() { results[i], failures[i] = CreateRootSnapshot(t.Context(), root, name, []byte(content), 0o600) })
	}
	wg.Wait()
	close(stop)
	watcher.Wait()

	publishers := 0
	for i, created := range results {
		if failures[i] != nil {
			t.Fatalf("concurrent creation failed: %v", failures[i])
		}
		if created {
			publishers++
		}
	}
	if publishers != 1 {
		t.Fatalf("expected exactly one publisher, got %d", publishers)
	}
	if shortSize >= 0 {
		t.Fatalf("a reader saw %s holding %d of %d bytes", name, shortSize, len(content))
	}
	if final, err := os.ReadFile(path); err != nil || string(final) != content {
		t.Fatalf("published file is not the staged content: %d bytes, %v", len(final), err)
	}
}
