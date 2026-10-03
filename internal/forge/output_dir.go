package forge

import (
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// generatedDir is a directory a forge generator writes its files into: the wiki output of
// GenerateWiki and the ADR log of TranscribeDiscussionToADR.
//
// A relative dir names a location inside root, and every write is confined to root through
// util.MkdirConfined, util.WriteFileConfined and util.WriteFileConfinedExclusive, every read
// through root's pinned handle. A repository that ships the directory, or
// one of its ancestors, as a link leading outside root is refused instead of followed, even
// when the link is swapped in after the confinement check (BUG-826). A relative dir that
// climbs out of root with ".." is refused too.
//
// An absolute dir is the operator's explicit choice of location. There is no root for it to
// escape, so it is written as given through the no-follow writers, which still refuse a link
// or a non-regular file at each destination.
type generatedDir struct {
	root string
	dir  string
}

// confined reports whether dir is written through root's pinned handle.
func (g generatedDir) confined() bool {
	return !filepath.IsAbs(g.dir)
}

// location is dir as the filesystem reaches it: joined onto root when dir is relative.
func (g generatedDir) location() string {
	if g.confined() {
		return filepath.Join(g.root, g.dir)
	}
	return g.dir
}

// path is where the generated file name lands.
func (g generatedDir) path(name string) string {
	return filepath.Join(g.location(), name)
}

// mkdir creates dir and its missing parents with perm as the ceiling on the leaf.
func (g generatedDir) mkdir(perm os.FileMode) error {
	if g.confined() {
		return util.MkdirConfined(g.root, g.dir, perm)
	}
	return util.MkdirSecure(g.dir, perm)
}

// write replaces the file name inside dir atomically, refusing a link at the destination.
func (g generatedDir) write(name string, data []byte, perm os.FileMode) error {
	if g.confined() {
		return util.WriteFileConfined(g.root, filepath.Join(g.dir, name), data, perm)
	}
	return util.WriteFileNoFollow(g.path(name), data, perm)
}

// writeExclusive creates the file name inside dir exclusively: an existing entry at name is
// refused with an error matching os.ErrExist and left as it is. A relative dir is written
// through root's pinned handle (util.WriteFileConfinedExclusive), an absolute one through
// util.WriteFileExclusive.
func (g generatedDir) writeExclusive(name string, data []byte, perm os.FileMode) error {
	if g.confined() {
		return util.WriteFileConfinedExclusive(g.root, filepath.Join(g.dir, name), data, perm)
	}
	return util.WriteFileExclusive(g.path(name), data, perm)
}

// readLimited reads the regular file name inside dir, up to limit bytes, through a pinned
// handle on dir (util.ReadLimitedIn). A relative dir is reached through root's handle, so it
// is confined like every write; an absolute dir is opened as given. The handle refuses a
// link that leads out of dir, and on Windows it lets a concurrent writer remove another link
// to the file being read.
func (g generatedDir) readLimited(name string, limit int64) ([]byte, error) {
	root, rel := g.root, g.dir
	if !g.confined() {
		root, rel = g.dir, "."
	}
	var data []byte
	err := util.InConfinedDirectory(root, rel, func(dir *os.Root) error {
		var readErr error
		data, readErr = util.ReadLimitedIn(dir, name, limit)
		return readErr
	})
	return data, err
}
