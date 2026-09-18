package devsync

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/topology"
)

const (
	archiveSuffix = ".tar.gz"
	partialSuffix = ".partial"
	// maxRepoDepth is how many folder levels below the dev folder are searched for repositories.
	maxRepoDepth = 4
	// maxDiscoveryDirs bounds the directories visited while searching for repositories (HISS-02).
	maxDiscoveryDirs = 100000
	// maxArchiveEntries bounds the entries one archive may hold, both written and read (HISS-02).
	maxArchiveEntries = 2000000
)

// cacheDirs are rebuildable build and dependency caches left out of every archive. The lists
// elsewhere in the tree serve other purposes (verification input discovery, inventory scans)
// and skip source folders such as vendor/ or .git/ that a copy of a project must keep.
var cacheDirs = map[string]bool{
	"node_modules": true, "target": true, ".venv": true, "venv": true, "__pycache__": true,
	".cache": true, "dist": true, "build": true, ".next": true, ".svelte-kit": true, ".gradle": true,
}

// unit is one folder written as one archive.
type unit struct {
	// rel is the unit's path below the dev folder, in host form.
	rel string
	// dir is the absolute folder archived.
	dir string
	// exclude lists paths below dir, in host form, archived separately as repositories.
	exclude map[string]bool
	// keepCaches archives cache-named folders too; the agent-state bundle has no build caches.
	keepCaches bool
	// folder marks a top-level folder that is not a repository.
	folder bool
}

// fingerprint summarises a unit cheaply enough to decide whether it changed since last push.
type fingerprint struct {
	Files       int64 `json:"files"`
	NewestMtime int64 `json:"newest_mtime_unix_nano"`
	Bytes       int64 `json:"bytes"`
}

func (f *fingerprint) add(info fs.FileInfo) {
	if !info.IsDir() {
		f.Files++
	}
	if info.Mode().IsRegular() {
		f.Bytes += info.Size()
	}
	f.NewestMtime = max(f.NewestMtime, info.ModTime().UnixNano())
}

// discoverUnits lists every repository below devDir (up to maxRepoDepth levels) and every
// top-level folder that is not a repository, the latter without the repositories inside it.
func discoverUnits(ctx context.Context, devDir string) ([]unit, error) {
	entries, err := os.ReadDir(devDir)
	if err != nil {
		return nil, fmt.Errorf("read dev folder: %w", err)
	}
	if len(entries) > maxDiscoveryDirs {
		return nil, fmt.Errorf("dev folder holds more than %d entries", maxDiscoveryDirs)
	}
	budget := maxDiscoveryDirs
	var units []unit
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(devDir, entry.Name())
		if topology.HasValidGitRepo(dir) {
			units = append(units, unit{rel: entry.Name(), dir: dir})
			continue
		}
		repos, err := findRepositories(ctx, dir, &budget)
		if err != nil {
			return nil, err
		}
		folder := unit{rel: entry.Name(), dir: dir, exclude: map[string]bool{}, folder: true}
		for _, repo := range repos {
			folder.exclude[repo] = true
			units = append(units, unit{rel: filepath.Join(entry.Name(), repo), dir: filepath.Join(dir, repo)})
		}
		units = append(units, folder)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].rel < units[j].rel })
	return units, nil
}

// findRepositories searches top breadth-first for repositories and returns their paths
// relative to top. It never descends into a repository, a cache folder or a symbolic link.
func findRepositories(ctx context.Context, top string, budget *int) ([]string, error) {
	queue := []pendingDir{{rel: ".", depth: 1}}
	var repos []string
	for len(queue) > 0 && *budget > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		*budget--
		found, deeper, err := scanFolder(top, current)
		if err != nil {
			return nil, err
		}
		repos, queue = append(repos, found...), append(queue, deeper...)
	}
	if len(queue) > 0 {
		return nil, fmt.Errorf("repository search exceeds %d folders", maxDiscoveryDirs)
	}
	return repos, nil
}

// pendingDir is a folder still to be searched, depth levels below the top-level folder.
type pendingDir struct {
	rel   string
	depth int
}

// scanFolder splits the child folders of current into repositories and folders to search next.
func scanFolder(top string, current pendingDir) (repos []string, deeper []pendingDir, err error) {
	children, err := os.ReadDir(filepath.Join(top, current.rel))
	if err != nil {
		return nil, nil, fmt.Errorf("search %s for repositories: %w", top, err)
	}
	for _, child := range children {
		if !child.IsDir() || child.Name() == ".git" || cacheDirs[child.Name()] {
			continue
		}
		rel := filepath.Join(current.rel, child.Name())
		if topology.HasValidGitRepo(filepath.Join(top, rel)) {
			repos = append(repos, rel)
		} else if current.depth < maxRepoDepth {
			deeper = append(deeper, pendingDir{rel: rel, depth: current.depth + 1})
		}
	}
	return repos, deeper, nil
}

// walkUnit visits, in lexical order, every entry of u that belongs in its archive.
func walkUnit(ctx context.Context, u unit, visit func(rel string, info fs.FileInfo) error) error {
	count := 0
	return filepath.WalkDir(u.dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, err := filepath.Rel(u.dir, path)
		if err != nil || rel == "." {
			return err
		}
		if entry.IsDir() && (u.exclude[rel] || (!u.keepCaches && cacheDirs[entry.Name()])) {
			return filepath.SkipDir
		}
		if count++; count > maxArchiveEntries {
			return fmt.Errorf("%s holds more than %d entries", u.dir, maxArchiveEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return visit(rel, info)
	})
}

// fingerprintUnit summarises u without reading file contents.
func fingerprintUnit(ctx context.Context, u unit) (fingerprint, error) {
	var fp fingerprint
	err := walkUnit(ctx, u, func(_ string, info fs.FileInfo) error {
		fp.add(info)
		return nil
	})
	return fp, err
}

// writeArchive streams u into w as a gzip-compressed tar and returns the fingerprint of what
// it wrote. On failure it returns without writing the end-of-archive trailers, so a failed
// stream never reads back as a complete archive.
func writeArchive(ctx context.Context, u unit, w io.Writer) (fingerprint, error) {
	compressed := gzip.NewWriter(w)
	archive := tar.NewWriter(compressed)
	var fp fingerprint
	err := walkUnit(ctx, u, func(rel string, info fs.FileInfo) error {
		fp.add(info)
		return writeEntry(archive, u.dir, rel, info)
	})
	if err != nil {
		return fp, err
	}
	return fp, errors.Join(archive.Close(), compressed.Close())
}

// writeEntry writes one directory, regular file or symbolic link. Sockets, FIFOs and devices
// have no portable archive form and are left out.
func writeEntry(archive *tar.Writer, root, rel string, info fs.FileInfo) error {
	header := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
	mode := info.Mode()
	switch {
	case mode.IsDir():
		header.Typeflag, header.Name = tar.TypeDir, header.Name+"/"
		return archive.WriteHeader(header)
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		header.Typeflag, header.Linkname = tar.TypeSymlink, filepath.ToSlash(target)
		return archive.WriteHeader(header)
	case mode.IsRegular():
		header.Typeflag, header.Size = tar.TypeReg, info.Size()
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		return copyFileInto(archive, filepath.Join(root, rel), info.Size())
	default:
		return nil
	}
}

// copyFileInto copies exactly size bytes of path, the size the header already declared.
// A file that shrank since it was listed fails the archive rather than padding it.
func copyFileInto(w io.Writer, path string, size int64) (err error) {
	// #nosec G304 -- path comes from walking the operator's own dev folder.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := io.CopyN(w, file, size); err != nil {
		return fmt.Errorf("copy %s: %w", path, err)
	}
	return nil
}
