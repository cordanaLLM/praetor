package hisscoverage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: an archive's comment is dropped, each file holds the lines up to the next marker,
// a nested path is staged under the root, and a CRLF marker line is still a marker.
func TestParseArchive_Positive_SplitsFilesAndStagesThem(t *testing.T) {
	archive := "a comment\n-- go.mod --\nmodule example.com/m\n-- pkg/p.go --\npackage p\r\n\n-- empty.txt --\r\n"
	files, err := parseArchive([]byte(archive))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []archiveFile{
		{"go.mod", []byte("module example.com/m\n")},
		{"pkg/p.go", []byte("package p\r\n\n")},
		{"empty.txt", nil},
	}
	if len(files) != len(want) {
		t.Fatalf("files = %+v, want %+v", files, want)
	}
	for i := range want {
		if files[i].name != want[i].name || string(files[i].data) != string(want[i].data) {
			t.Errorf("file %d = %q %q, want %q %q", i, files[i].name, files[i].data, want[i].name, want[i].data)
		}
	}
	root := t.TempDir()
	if err := stageArchive(root, []byte(archive)); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "pkg", "p.go")); err != nil || string(data) != "package p\r\n\n" {
		t.Fatalf("staged pkg/p.go = %q, %v", data, err)
	}
}

// Negative: an archive without a marker, a name that leaves the root, an absolute or unclean
// name, an empty name and a repeated name are refused, and staging refuses them too.
func TestParseArchive_Negative_RefusesUnsafeArchives(t *testing.T) {
	cases := map[string]string{
		"no marker":  "package p\n",
		"parent":     "-- ../escape.go --\npackage p\n",
		"absolute":   "-- /etc/passwd --\nx\n",
		"unclean":    "-- a/./b.go --\nx\n",
		"empty name": "--   --\nx\n",
		"repeated":   "-- a.go --\nx\n-- a.go --\ny\n",
	}
	for name, archive := range cases {
		if _, err := parseArchive([]byte(archive)); !errors.Is(err, errArchive) {
			t.Errorf("%s: parse error = %v, want errArchive", name, err)
		}
		if err := stageArchive(t.TempDir(), []byte(archive)); !errors.Is(err, errArchive) {
			t.Errorf("%s: stage error = %v, want errArchive", name, err)
		}
	}
}

// Boundary: exactly maxArchiveFiles files are accepted and one more is refused; a line that
// only resembles a marker is file content.
func TestParseArchive_Boundary_FileBoundAndNearMarkers(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxArchiveFiles; i++ {
		sb.WriteString("-- f" + strings.Repeat("x", i) + ".go --\npackage p\n")
	}
	if files, err := parseArchive([]byte(sb.String())); err != nil || len(files) != maxArchiveFiles {
		t.Fatalf("%d files must be accepted: %d, %v", maxArchiveFiles, len(files), err)
	}
	sb.WriteString("-- over.go --\npackage p\n")
	if _, err := parseArchive([]byte(sb.String())); !errors.Is(err, errArchive) {
		t.Fatalf("file %d must be refused, got %v", maxArchiveFiles+1, err)
	}
	files, err := parseArchive([]byte("-- a.go --\n--a.go --\n-- b.go--\n"))
	if err != nil || len(files) != 1 || string(files[0].data) != "--a.go --\n-- b.go--\n" {
		t.Fatalf("near markers are content: %+v, %v", files, err)
	}
}

// TestVerifyStagesArchiveFixtures: an archive fixture is staged as the files it holds, so a
// claim backed by a recursive Go file inside one holds (positive), while the same archive with
// the Go file renamed to a non-Go extension backs nothing (negative).
func TestVerifyStagesArchiveFixtures(t *testing.T) {
	archive := "-- go.mod --\nmodule example.com/m\n-- p/p.go --\n" + recursiveGo
	root := corpusRoot(t, "HISS-01", "go", bucketPositive, "recursion.txtar", archive)
	report, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Passed() || report.Fixtures != 1 {
		t.Fatalf("an archive fixture must be staged as its files: %+v", report.Findings)
	}
	root = corpusRoot(t, "HISS-01", "go", bucketPositive, "recursion.txtar", strings.Replace(archive, "p/p.go", "p/p.txt", 1))
	report, err = Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed() {
		t.Fatal("an archive whose Go file is staged under another extension backs no claim")
	}
	root = corpusRoot(t, "HISS-01", "go", bucketPositive, "broken.txtar", "no marker\n")
	if _, err := Verify(t.Context(), root, catalogFor("HISS-01", "go", StatePartial)); !errors.Is(err, errArchive) {
		t.Fatalf("a malformed archive fails the replay, got %v", err)
	}
}
