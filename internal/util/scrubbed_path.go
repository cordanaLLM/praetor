package util

import (
	"os"
	"path/filepath"
	"strings"
)

// posixToolDirectories are the directories a POSIX host keeps its helper programs in.
// They are candidates, not requirements: each is included only where it exists.
var posixToolDirectories = []string{"/usr/bin", "/bin"}

// ScrubbedToolPath returns the minimal PATH that can still find an already-resolved tool
// and the helpers it shells out to.
//
// Callers run a subprocess in a deliberately scrubbed environment so it cannot see the
// caller's configuration. The earlier form of that scrub appended ":/usr/bin:/bin" with a
// literal colon. os.PathListSeparator is ';' on Windows, so the whole value collapsed into
// one malformed entry and the tool could not be found at all. Joining with the platform's
// own separator fixes that.
//
// The POSIX directories are kept rather than dropped, and only where they exist: git
// shells out to helpers of its own, so removing them would trade a Windows failure for a
// Linux one. On Windows they are absent and contribute nothing.
func ScrubbedToolPath(tool string) string {
	entries := []string{filepath.Dir(tool)}
	for _, dir := range posixToolDirectories {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			entries = append(entries, dir)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
}
