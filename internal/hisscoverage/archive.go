package hisscoverage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	slashpath "path"
	"path/filepath"
)

// A rule that reads more than one file, such as the Go HISS-02 pass that checks a call against
// the module's go.mod, needs a fixture of several files. Committing those files as a directory
// would put a real go.mod into the tree, where dependency tooling reads it as one of the
// repository's own manifests. A fixture named *.txtar is therefore one file in the txtar format
// of golang.org/x/tools/txtar, and the replay stages each file it holds at its path under the
// fixture root before the scan.
//
// The format: an optional comment, then each file introduced by a marker line "-- NAME --"
// holding everything up to the next marker. NAME is a slash-separated relative path.

const (
	// archiveExt marks a fixture that holds several files.
	archiveExt = ".txtar"
	// maxArchiveFiles bounds the files one archive fixture holds (HISS-02).
	maxArchiveFiles = 16
	// maxArchiveLines bounds the lines one archive fixture is read over (HISS-02).
	maxArchiveLines = 1 << 16
)

// archiveFile is one file of an archive fixture.
type archiveFile struct {
	name string
	data []byte
}

// errArchive reports an archive fixture the replay refuses to stage.
var errArchive = errors.New("hisscoverage: malformed archive fixture")

// parseArchive splits an archive fixture into its files. It refuses an archive without files,
// with more than maxArchiveFiles or maxArchiveLines, a file named twice, and a name that is
// empty, absolute, unclean or leaves the fixture root.
func parseArchive(data []byte) ([]archiveFile, error) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > maxArchiveLines {
		return nil, fmt.Errorf("%w: more than %d lines", errArchive, maxArchiveLines)
	}
	var files []archiveFile
	seen := make(map[string]bool)
	for i := 0; i < len(lines); i++ {
		name, marker := archiveMarker(lines[i])
		if !marker {
			if len(files) > 0 {
				last := &files[len(files)-1]
				last.data = append(last.data, lines[i]...)
			}
			continue
		}
		if err := checkArchiveName(name, seen, len(files)); err != nil {
			return nil, err
		}
		seen[name] = true
		files = append(files, archiveFile{name: name})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no file marker", errArchive)
	}
	return files, nil
}

// archiveMarker returns the file name a "-- NAME --" marker line introduces.
func archiveMarker(line []byte) (string, bool) {
	text := string(bytes.TrimRight(line, "\r\n"))
	if len(text) < len("-- --") || text[:3] != "-- " || text[len(text)-3:] != " --" {
		return "", false
	}
	// "-- --" shares its one space between prefix and suffix: it names nothing, so checkArchiveName
	// refuses it as an empty name.
	end := max(3, len(text)-3)
	return string(bytes.TrimSpace([]byte(text[3:end]))), true
}

// checkArchiveName refuses a name that cannot be staged under the fixture root, a repeated
// name, and a file past maxArchiveFiles; count is the files already read.
func checkArchiveName(name string, seen map[string]bool, count int) error {
	switch {
	case count >= maxArchiveFiles:
		return fmt.Errorf("%w: more than %d files", errArchive, maxArchiveFiles)
	case name == "" || slashpath.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)):
		return fmt.Errorf("%w: file name %q is not a clean relative path", errArchive, name)
	case seen[name]:
		return fmt.Errorf("%w: file %q named twice", errArchive, name)
	}
	return nil
}

// stageArchive writes every file of an archive fixture below root.
func stageArchive(root string, data []byte) error {
	files, err := parseArchive(data)
	if err != nil {
		return err
	}
	for i := 0; i < len(files); i++ {
		target := filepath.Join(root, filepath.FromSlash(files[i].name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("stage %s: %w", files[i].name, err)
		}
		if err := os.WriteFile(target, files[i].data, 0o600); err != nil {
			return fmt.Errorf("stage %s: %w", files[i].name, err)
		}
	}
	return nil
}
