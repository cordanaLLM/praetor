package repairrun

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxWorkspaceBytes = 16 << 20
const maxWorkspaceFiles = 20000

type sourceFile struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type sourceManifest map[string]sourceFile

func materialize(ctx context.Context, cfg Config, attempt *os.Root, directory string) (sourceManifest, error) {
	source, err := openDirectory(cfg.SourceRoot)
	if err != nil {
		return nil, err
	}
	if err := source.Close(); err != nil {
		return nil, err
	}
	kind, err := runGit(ctx, cfg.SourceRoot, 128, "cat-file", "-t", cfg.SourceSHA)
	if err != nil || string(kind) != "commit\n" {
		return nil, errors.New("source_sha must identify an immutable commit")
	}
	data, err := runGit(ctx, cfg.SourceRoot, maxWorkspaceBytes, "ls-tree", "-rz", cfg.SourceSHA)
	if err != nil {
		return nil, errors.New("pinned source tree listing failed")
	}
	entries := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	if len(entries) < 1 || len(entries) > maxWorkspaceFiles {
		return nil, errors.New("source tree entry bound exceeded")
	}
	if err := attempt.Mkdir("candidate", 0o700); err != nil {
		return nil, err
	}
	root, err := openDirectory(filepath.Join(directory, "candidate"))
	if err != nil {
		return nil, err
	}
	manifest, extractErr := extractSource(ctx, cfg, root, entries)
	return manifest, errors.Join(extractErr, root.Close())
}

func extractSource(ctx context.Context, cfg Config, root *os.Root, entries []string) (sourceManifest, error) {
	manifest, total := sourceManifest{}, 0
	for _, entry := range entries {
		size, err := extractBlob(ctx, cfg, root, manifest, entry, maxWorkspaceBytes-total)
		if err != nil {
			return nil, err
		}
		total += size
		if total > maxWorkspaceBytes {
			return nil, errors.New("source tree byte bound exceeded")
		}
	}
	return manifest, nil
}

func extractBlob(ctx context.Context, cfg Config, root *os.Root, manifest sourceManifest, entry string, remaining int) (int, error) {
	header, path, ok := strings.Cut(entry, "\t")
	fields := strings.Fields(header)
	if !ok || !archivePath(path) || !validBlobHeader(fields) {
		return 0, errors.New("source tree contains unsafe path, link or special entry")
	}
	if _, found := manifest[path]; found {
		return 0, errors.New("source tree repeats a path")
	}
	data, err := runGit(ctx, cfg.SourceRoot, min(8<<20, remaining), "cat-file", "blob", fields[2])
	if err != nil {
		return 0, errors.New("pinned source blob extraction failed")
	}
	if err := root.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	if err := writeNew(root, path, data); err != nil {
		return 0, err
	}
	mode := uint32(0o600)
	if fields[0] == "100755" {
		mode = 0o700
	}
	if err := root.Chmod(path, os.FileMode(mode)); err != nil {
		return 0, err
	}
	manifest[path] = sourceFile{SHA256: bytesSHA(data), Mode: mode}
	return len(data), nil
}

func archivePath(path string) bool {
	return filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != ".git" && !strings.HasPrefix(path, ".git/")
}

func validBlobHeader(fields []string) bool {
	return len(fields) == 3 && (fields[0] == "100644" || fields[0] == "100755") && fields[1] == "blob" && sourceSHA.MatchString(fields[2])
}

func snapshotCandidate(ctx context.Context, root *os.Root) (sourceManifest, error) {
	manifest, dirs, total := sourceManifest{}, []string{"."}, int64(0)
	for index := 0; index < len(dirs) && index < maxWorkspaceFiles; index++ {
		next, size, err := snapshotDirectory(ctx, root, dirs[index], manifest)
		if err != nil {
			return nil, err
		}
		dirs, total = append(dirs, next...), total+size
		if len(dirs)+len(manifest) > maxWorkspaceFiles || total > maxWorkspaceBytes {
			return nil, errors.New("candidate exceeded source bounds")
		}
	}
	return manifest, nil
}

func snapshotDirectory(ctx context.Context, root *os.Root, path string, manifest sourceManifest) ([]string, int64, error) {
	dir, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	entries, readErr := dir.ReadDir(maxWorkspaceFiles + 1)
	if err := errors.Join(readErr, dir.Close()); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	if len(entries) > maxWorkspaceFiles {
		return nil, 0, errors.New("candidate directory exceeded entry bound")
	}
	dirs, total := []string{}, int64(0)
	for _, entry := range entries {
		name := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		data, err := readFile(ctx, root, name, 8<<20, false)
		if err != nil {
			return nil, 0, err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return nil, 0, err
		}
		manifest[name] = sourceFile{SHA256: bytesSHA(data), Mode: uint32(info.Mode().Perm())}
		total += int64(len(data))
	}
	return dirs, total, nil
}
