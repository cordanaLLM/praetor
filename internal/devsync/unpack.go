package devsync

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafeEntry reports an archive entry that would land, or point, outside the target.
var ErrUnsafeEntry = errors.New("devsync: archive entry escapes the target directory")

// maxLinkSegments bounds the path segments examined per symbolic link (HISS-02).
const maxLinkSegments = 4096

// pendingLink is a symbolic link created after every other entry, once the full set of
// links in the archive is known.
type pendingLink struct{ name, target string }

// extractArchive unpacks a gzip-compressed tar stream into dir. Every write goes through an
// os.Root, entry names must be local paths, and a link may only point inside dir without
// passing through another link, so nothing lands or points outside dir.
func extractArchive(ctx context.Context, r io.Reader, dir string) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	decompressed, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, decompressed.Close()) }()
	links, err := extractEntries(ctx, root, tar.NewReader(decompressed), maxArchiveEntries)
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, decompressed); err != nil {
		return fmt.Errorf("verify archive trailer: %w", err)
	}
	return createLinks(root, links)
}

// extractEntries writes every entry but the links, which it returns. An archive holding more
// than limit entries fails before the first entry past the limit is written.
func extractEntries(ctx context.Context, root *os.Root, archive *tar.Reader, limit int) ([]pendingLink, error) {
	var links []pendingLink
	for i := 0; i <= limit; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return links, nil
		}
		if err != nil {
			return nil, err
		}
		if i == limit {
			break
		}
		name, err := entryName(header.Name)
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeSymlink {
			links = append(links, pendingLink{name: name, target: header.Linkname})
			continue
		}
		if err := extractEntry(root, archive, header, name); err != nil {
			return nil, fmt.Errorf("extract %s: %w", header.Name, err)
		}
	}
	return nil, fmt.Errorf("archive holds more than %d entries", limit)
}

// entryName converts an archive name to a local host path, refusing absolute names and any
// ".." segment even where it would resolve back inside.
func entryName(raw string) (string, error) {
	slashed := strings.TrimSuffix(filepath.ToSlash(raw), "/")
	local := filepath.FromSlash(slashed)
	if !filepath.IsLocal(local) || containsSegment(slashed, "..") {
		return "", fmt.Errorf("%w: %q", ErrUnsafeEntry, raw)
	}
	return local, nil
}

func containsSegment(slashed, segment string) bool {
	parts := strings.Split(slashed, "/")
	for i := 0; i < len(parts) && i < maxLinkSegments; i++ {
		if parts[i] == segment {
			return true
		}
	}
	return false
}

func extractEntry(root *os.Root, archive io.Reader, header *tar.Header, name string) (err error) {
	switch header.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(name, dirMode(header.Mode))
	case tar.TypeReg:
		if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode&0o777|0o400))
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		_, err = io.Copy(file, archive)
		return err
	default:
		return fmt.Errorf("unsupported entry type %q", header.Typeflag)
	}
}

// dirMode keeps a directory's archived permissions but always lets the owner fill it.
func dirMode(mode int64) os.FileMode { return os.FileMode(mode&0o777 | 0o700) }

func createLinks(root *os.Root, links []pendingLink) error {
	names := make(map[string]bool, len(links))
	for _, link := range links {
		names[filepath.ToSlash(link.name)] = true
	}
	for _, link := range links {
		if !linkStaysInside(filepath.ToSlash(link.name), link.target, names) {
			return fmt.Errorf("%w: link %s -> %s", ErrUnsafeEntry, link.name, link.target)
		}
		if err := root.MkdirAll(filepath.Dir(link.name), 0o700); err != nil {
			return err
		}
		if err := root.Symlink(filepath.FromSlash(link.target), link.name); err != nil {
			return fmt.Errorf("create link %s: %w", link.name, err)
		}
	}
	return nil
}

// linkStaysInside resolves target lexically from the link's own folder. The result must stay
// inside the root, and neither the link's folder nor any step of the target may pass through
// another link of the archive: a link's meaning then never depends on where another points.
func linkStaysInside(name, target string, links map[string]bool) bool {
	target = filepath.ToSlash(target)
	local := filepath.FromSlash(target)
	if target == "" || path.IsAbs(target) || filepath.IsAbs(local) || filepath.VolumeName(local) != "" {
		return false
	}
	var folder []string
	if parent := path.Dir(name); parent != "." {
		folder = strings.Split(parent, "/")
	}
	if len(folder) > maxLinkSegments {
		return false
	}
	for i := 1; i <= len(folder); i++ {
		if links[strings.Join(folder[:i], "/")] {
			return false
		}
	}
	return resolvesInside(folder, strings.Split(target, "/"), links)
}

// resolvesInside applies target segments to the folder stack, failing on a step above the
// root or through another link.
func resolvesInside(stack, segments []string, links map[string]bool) bool {
	if len(segments) > maxLinkSegments {
		return false
	}
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return false
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, segment)
			if links[strings.Join(stack, "/")] {
				return false
			}
		}
	}
	return true
}
