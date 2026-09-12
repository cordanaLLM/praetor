package devcontainer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestBootstrapArchiveRejectsUntrustedContents(t *testing.T) {
	files, err := captureBootstrapSource(t.Context(), bootstrapSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]bootstrapSourceFile) []bootstrapSourceFile{
		"traversal": func(f []bootstrapSourceFile) []bootstrapSourceFile { f[0].Name = "../outside.go"; return f },
		"duplicate": func(f []bootstrapSourceFile) []bootstrapSourceFile { return append(f, f[0]) },
		"oversized-file": func(f []bootstrapSourceFile) []bootstrapSourceFile {
			f[0] = bootstrapSourceFile{Name: "LICENSE", Data: bytes.Repeat([]byte("x"), contextopt.MaxSourceBytes+1)}
			return f
		},
		"missing-inputs": func([]bootstrapSourceFile) []bootstrapSourceFile { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			archive, err := encodeBootstrapArchive(change(append([]bootstrapSourceFile(nil), files...)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeBootstrapArchive(t.Context(), archive); err == nil {
				t.Fatal("untrusted archive accepted")
			}
		})
	}
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	archive[len(archive)-8] ^= 1
	if _, err := decodeBootstrapArchive(t.Context(), archive); err == nil {
		t.Fatal("corrupted gzip checksum accepted")
	}
}

func TestBootstrapArchiveRejectsLinksAndTrailingBytes(t *testing.T) {
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "link.go", Typeflag: tar.TypeSymlink, Linkname: "/outside", Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBootstrapArchive(t.Context(), data.Bytes()); err == nil {
		t.Fatal("archive link accepted")
	}
	files, err := captureBootstrapSource(t.Context(), bootstrapSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBootstrapArchive(t.Context(), append(encoded, []byte("trailing")...)); err == nil {
		t.Fatal("trailing compressed content accepted")
	}
}

func TestBootstrapWriteBoundariesPreflightAllCompanions(t *testing.T) {
	for name, change := range map[string]func(*Bundle){
		"oversized-json": func(b *Bundle) { b.Config.Name = strings.Repeat("x", contextopt.MaxSourceBytes+1) },
		"absent-config":  func(b *Bundle) { b.Config = nil },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := preparedBootstrap(t)
			change(bundle)
			root := t.TempDir()
			if err := WriteBundle(t.Context(), filepath.Join(root, "new", "devcontainer.json"), bundle, false); err == nil {
				t.Fatal("invalid bundle written")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid JSON wrote companions")
			}
		})
	}
	bundle := preparedBootstrap(t)
	root := t.TempDir()
	path := filepath.Join(root, "devcontainer.json")
	if err := WriteDevContainer(t.Context(), path, bundle.Config); err == nil {
		t.Fatal("legacy writer accepted config without companions")
	}
	writeBootstrapFile(t, root, bootstrapDockerfile, "operator owned\n")
	if err := WriteBundle(t.Context(), path, bundle, false); err == nil {
		t.Fatal("companion conflict ignored")
	}
	if _, err := os.Stat(filepath.Join(root, bootstrapPartName(0))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late conflict left earlier companion behind")
	}
	if err := WriteBundle(t.Context(), path, bundle, true); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapWriteRejectsSymlinkCompanionEvenWithForce(t *testing.T) {
	bundle := preparedBootstrap(t)
	root, outside := t.TempDir(), t.TempDir()
	writeBootstrapFile(t, outside, "owned", "outside content\n")
	if err := os.Symlink(filepath.Join(outside, "owned"), filepath.Join(root, bootstrapDockerfile)); err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(t.Context(), filepath.Join(root, "devcontainer.json"), bundle, true); err == nil {
		t.Fatal("forced write followed companion symlink")
	}
	got, err := os.ReadFile(filepath.Join(outside, "owned"))
	if err != nil || string(got) != "outside content\n" {
		t.Fatal("outside file changed")
	}
}
