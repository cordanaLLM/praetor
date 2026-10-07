package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// OffloadThresholdBytes is the default size above which one text item is written to
	// the offload directory and replaced by a pointer line.
	OffloadThresholdBytes = 16 << 10
	// OffloadMaxFiles bounds the offload directory (HISS-02); the oldest files go first.
	OffloadMaxFiles = 256
	// OffloadHeadBytes is how much of the text the pointer line carries inline.
	OffloadHeadBytes = 400
	// OffloadDir is the repository-relative offload directory, always with forward slashes.
	OffloadDir = ".standards/cache/mcp-out"
	// OffloadMaxReadBytes bounds one OffloadRead; it stays below the default threshold so
	// a read-back is never offloaded again.
	OffloadMaxReadBytes = 12 << 10

	offloadExt = ".txt"
)

var (
	// ErrOffloadDigest reports a read-back digest that is not 64 lowercase hex digits.
	ErrOffloadDigest = errors.New("mcp: offload digest must be 64 lowercase hex digits")
	// ErrOffloadRange reports a read-back offset or limit outside the stored text.
	ErrOffloadRange = errors.New("mcp: offload read range is invalid")

	offloadDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Offloader moves large tool output out of the model context. Text is stored
// content-addressed below Root, so equal bytes give the same file and the same pointer.
type Offloader struct {
	// Root is the repository root the offload directory sits in.
	Root string
	// Threshold is the size in bytes above which a text item is offloaded; zero selects
	// OffloadThresholdBytes.
	Threshold int
	// MaxFiles bounds the stored files; zero selects OffloadMaxFiles.
	MaxFiles int
}

func (o Offloader) threshold() int {
	if o.Threshold > 0 {
		return o.Threshold
	}
	return OffloadThresholdBytes
}

func (o Offloader) maxFiles() int {
	if o.MaxFiles > 0 {
		return o.MaxFiles
	}
	return OffloadMaxFiles
}

// Apply returns res with every text item above the threshold replaced by a pointer line.
// Call it on the output of SanitizeResult: the stored bytes are the sanitized bytes. res
// is never modified. A write failure is returned, never served inline (no silent fallback).
func (o Offloader) Apply(res *ToolResult) (*ToolResult, error) {
	if res == nil {
		return nil, ErrNilResult
	}
	out := &ToolResult{Content: make([]ContentItem, len(res.Content)), IsError: res.IsError}
	for i := 0; i < len(res.Content); i++ {
		item := res.Content[i]
		if len(item.Text) > o.threshold() {
			pointer, err := o.store(item.Text)
			if err != nil {
				return nil, err
			}
			item.Text = pointer
		}
		out.Content[i] = item
	}
	return out, nil
}

// store writes text content-addressed and returns its pointer line.
func (o Offloader) store(text string) (string, error) {
	sum := sha256.Sum256([]byte(text))
	digest := hex.EncodeToString(sum[:])
	rel := filepath.Join(filepath.FromSlash(OffloadDir), digest+offloadExt)
	if err := util.MkdirConfined(o.Root, filepath.FromSlash(OffloadDir), util.SecureDirPerm); err != nil {
		return "", fmt.Errorf("mcp: create offload directory: %w", err)
	}
	err := util.WriteFileConfinedExclusive(o.Root, rel, []byte(text), util.SecureFilePerm)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrExist):
		if err := o.touch(rel); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("mcp: write offloaded output: %w", err)
	}
	if err := o.prune(digest); err != nil {
		return "", err
	}
	return pointerLine(digest, text), nil
}

// touch refreshes the modification time of an existing file so cleanup keeps recent output.
func (o Offloader) touch(rel string) error {
	full, err := util.ConfinePath(o.Root, rel)
	if err != nil {
		return fmt.Errorf("mcp: resolve offloaded output: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(full, now, now); err != nil {
		return fmt.Errorf("mcp: refresh offloaded output time: %w", err)
	}
	return nil
}

// pointerLine renders the one-line replacement for offloaded text. The path is
// repo-relative with forward slashes on every platform; the head is quoted so the line
// stays one line.
func pointerLine(digest, text string) string {
	head := text
	if len(head) > OffloadHeadBytes {
		head = head[:OffloadHeadBytes]
		for len(head) > 0 && !utf8.ValidString(head) {
			head = head[:len(head)-1]
		}
	}
	return fmt.Sprintf("[offloaded] path=%s bytes=%d sha256=%s head=%s",
		path.Join(OffloadDir, digest+offloadExt), len(text), digest, strconv.Quote(head))
}

type offloadEntry struct {
	name string
	mod  time.Time
}

// prune deletes the oldest stored files beyond maxFiles, never the file named keep.
func (o Offloader) prune(keep string) error {
	dir, err := util.ConfinePath(o.Root, filepath.FromSlash(OffloadDir))
	if err != nil {
		return fmt.Errorf("mcp: resolve offload directory: %w", err)
	}
	stored, err := listStored(dir)
	if err != nil {
		return err
	}
	sort.Slice(stored, func(a, b int) bool {
		if !stored[a].mod.Equal(stored[b].mod) {
			return stored[a].mod.Before(stored[b].mod)
		}
		return stored[a].name < stored[b].name
	})
	for i := 0; i < len(stored) && len(stored)-i > o.maxFiles(); i++ {
		if stored[i].name == keep+offloadExt {
			continue
		}
		if err := os.Remove(filepath.Join(dir, stored[i].name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("mcp: remove old offloaded output: %w", err)
		}
	}
	return nil
}

// listStored lists the offloaded files in dir; anything else in it is left alone.
func listStored(dir string) ([]offloadEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("mcp: list offload directory: %w", err)
	}
	var stored []offloadEntry
	for i := 0; i < len(entries); i++ {
		name := entries[i].Name()
		digest, isStored := strings.CutSuffix(name, offloadExt)
		if !isStored || !offloadDigestPattern.MatchString(digest) || entries[i].IsDir() {
			continue
		}
		info, err := entries[i].Info()
		if err != nil {
			continue
		}
		stored = append(stored, offloadEntry{name: name, mod: info.ModTime()})
	}
	return stored, nil
}

// Read returns up to limit bytes of the offloaded text with the given digest, starting at
// offset. The digest is validated before it touches a path, so only files in the offload
// directory can be read. limit is clamped to OffloadMaxReadBytes (zero selects it); the
// slice is moved to rune boundaries so it never splits a character. next is the offset to
// continue from (the aligned end); total is the full size.
func (o Offloader) Read(digest string, offset, limit int) (chunk string, next, total int, err error) {
	if !offloadDigestPattern.MatchString(digest) {
		return "", 0, 0, ErrOffloadDigest
	}
	if offset < 0 || limit < 0 {
		return "", 0, 0, fmt.Errorf("%w: offset %d, limit %d", ErrOffloadRange, offset, limit)
	}
	if limit == 0 || limit > OffloadMaxReadBytes {
		limit = OffloadMaxReadBytes
	}
	rel := filepath.Join(filepath.FromSlash(OffloadDir), digest+offloadExt)
	data, err := util.ReadConfinedLimited(o.Root, rel, MaxResultTextBytes)
	if err != nil {
		return "", 0, 0, fmt.Errorf("mcp: read offloaded output %s: %w", digest, err)
	}
	total = len(data)
	if offset > total {
		return "", 0, total, fmt.Errorf("%w: offset %d beyond %d bytes", ErrOffloadRange, offset, total)
	}
	chunk, next = runeAligned(string(data), offset, min(offset+limit, total))
	return chunk, next, total, nil
}

// runeAligned returns text[start:end] with both ends moved inward to rune boundaries, and
// the aligned end.
func runeAligned(text string, start, end int) (string, int) {
	for start < end && start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	for end < len(text) && end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	if end == start && start < len(text) {
		// A limit smaller than one rune still advances by that rune.
		end = start + 1
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
	}
	return text[start:end], end
}
