package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// OffloadThresholdBytes is the default size of all text items of one result above which
	// the largest items are written to the offload directory and replaced by pointer lines.
	OffloadThresholdBytes = 16 << 10
	// OffloadDisabled is the Threshold value that turns offloading off: every result is
	// served inline, up to the sanitizer bound.
	OffloadDisabled = -1
	// OffloadMaxFiles bounds the offload directory (HISS-02); the oldest files go first.
	OffloadMaxFiles = 256
	// OffloadHeadBytes is how much of the text the pointer line carries inline.
	OffloadHeadBytes = 400
	// OffloadCacheDir is the repository-relative cache directory, always with forward slashes.
	OffloadCacheDir = ".standards/cache"
	// OffloadDir is the repository-relative offload directory.
	OffloadDir = OffloadCacheDir + "/mcp-out"
	// OffloadMaxReadBytes bounds one OffloadRead; it stays below the default threshold so
	// a read-back is never offloaded again.
	OffloadMaxReadBytes = 12 << 10

	offloadExt = ".txt"

	// OffloadReadToolName names the tool that reads offloaded output back; pointer lines name
	// it, and its own output is never offloaded.
	OffloadReadToolName = "standards_output_read"

	// offloadGitTimeout bounds the one git query that proves the cache directory is ignored
	// (HISS-02).
	offloadGitTimeout = 10 * time.Second
	// offloadProbeDigest names the path that query asks about; any digest-shaped name is
	// ignored by the same rule.
	offloadProbeDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	// offloadRecordName names the record of the digests this server wrote, in the offload
	// directory. Read serves only a digest the record lists.
	offloadRecordName = "written.idx"
	// offloadRecordKeep bounds the record to this many digests per allowed stored file.
	offloadRecordKeep = 2
	// offloadRecordMaxBytes bounds one read of the record: offloadRecordKeep times the default
	// file cap of 64-digit lines, rounded up. A larger record is refused as foreign.
	offloadRecordMaxBytes = 64 << 10
)

var (
	// ErrOffloadDigest reports a read-back digest that is not 64 lowercase hex digits.
	ErrOffloadDigest = errors.New("mcp: offload digest must be 64 lowercase hex digits")
	// ErrOffloadRange reports a read-back offset or limit outside the stored text.
	ErrOffloadRange = errors.New("mcp: offload read range is invalid")
	// ErrOffloadMissing reports a digest with no stored file, such as output evicted by the
	// file cap or never offloaded.
	ErrOffloadMissing = errors.New("mcp: offloaded output is not stored (evicted or never offloaded); rerun the tool")
	// ErrOffloadUnreadable reports a stored file that could not be read back. The cause is
	// dropped on purpose: it carries an absolute path, and the model sees repository-relative
	// paths only.
	ErrOffloadUnreadable = errors.New("mcp: offloaded output could not be read")

	offloadDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Offloader moves large tool output out of the model context. Text is stored
// content-addressed below Root, so equal bytes give the same file and the same pointer.
type Offloader struct {
	// Root is the repository root the offload directory sits in.
	Root string
	// Threshold is the size in bytes of all text items of one result above which items are
	// offloaded, largest first; zero selects OffloadThresholdBytes and OffloadDisabled
	// turns offloading off.
	Threshold int
	// MaxFiles bounds the stored files; zero selects OffloadMaxFiles.
	MaxFiles int
}

func (o Offloader) threshold() int {
	if o.Threshold != 0 {
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

// Serve makes the result of the tool named tool safe to serve: sanitize first, then offload,
// so the stored bytes are the sanitized bytes. A successful read-back (OffloadReadToolName) is
// sanitized too but never offloaded: offloading it would point the client at the file it just
// read. Read verifies that the stored file is one this server wrote and a sanitizer fixed
// point, so sanitizing the chunk changes it only where the cut created a phrase the whole text
// does not contain ("xignore previous instructions" cut after the x). There the served chunk
// is the neutralized text, not the stored bytes: the byte fidelity of a chunk is traded for
// never serving an injection phrase a cut exposed.
func (o Offloader) Serve(ctx context.Context, tool string, res *ToolResult) (*ToolResult, error) {
	if res == nil {
		return nil, ErrNilResult
	}
	safe, err := SanitizeResult(res)
	if err != nil {
		return nil, err
	}
	if tool == OffloadReadToolName && !res.IsError {
		return safe, nil
	}
	return o.Apply(ctx, safe)
}

// Apply returns res with the largest text items replaced by pointer lines until the text
// of the whole call is at most the threshold. Call it on the output of SanitizeResult: the
// stored bytes are the sanitized bytes. res is never modified. A write failure is returned,
// never served inline (no silent fallback). Where git does not ignore the cache directory,
// nothing is written and res is served inline with a notice item that names the
// substitution, so offloaded output can never turn up as untracked files.
func (o Offloader) Apply(ctx context.Context, res *ToolResult) (*ToolResult, error) {
	if res == nil {
		return nil, ErrNilResult
	}
	out := &ToolResult{Content: slices.Clone(res.Content), IsError: res.IsError}
	total := 0
	for i := 0; i < len(out.Content); i++ {
		total += len(out.Content[i].Text)
	}
	if o.threshold() < 0 || total <= o.threshold() {
		return out, nil
	}
	skipped, err := o.prepare(ctx)
	if err != nil {
		return nil, err
	}
	if skipped != "" {
		return withOffloadNotice(out, skipped)
	}
	return out, o.offloadLargest(out, total)
}

// offloadLargest replaces text items of out, largest first, until total, the byte count of
// all items, is at most the threshold. Equal sizes go in item order.
func (o Offloader) offloadLargest(out *ToolResult, total int) error {
	order := make([]int, len(out.Content))
	for i := 0; i < len(order); i++ {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len(out.Content[order[a]].Text) > len(out.Content[order[b]].Text)
	})
	for i := 0; i < len(order) && total > o.threshold(); i++ {
		item := &out.Content[order[i]]
		pointer, err := o.store(item.Text)
		if err != nil {
			return err
		}
		total += len(pointer) - len(item.Text)
		item.Text = pointer
	}
	return nil
}

// withOffloadNotice appends the notice that offloading was skipped, as an item of its own so
// a structured first item stays parseable.
func withOffloadNotice(out *ToolResult, reason string) (*ToolResult, error) {
	if len(out.Content) >= MaxResultContentItems {
		return nil, fmt.Errorf("%w: no room for the offload notice (%s)", ErrResultTooLarge, reason)
	}
	out.Content = append(out.Content, ContentItem{Type: "text", Text: "[offload skipped] " + reason +
		"; the output above is inline. Add /" + OffloadCacheDir + "/ to .gitignore (praetorctl adopt writes it), then retry."})
	return out, nil
}

// gitWorktreePresent is the work-tree detection prepare uses; a variable so a test can inject
// an error no portable fixture produces (a permission failure on an ancestor).
var gitWorktreePresent = util.GitWorktreePresent

// prepare creates the offload directory and its self-ignoring .gitignore, then proves git
// ignores the cache path. A non-empty reason means offloading must not write; err is an I/O
// failure, including a failed look for the work tree (fail closed: an unreadable ancestor is
// not a root outside git). A root outside any git work tree has no status to pollute and passes.
func (o Offloader) prepare(ctx context.Context) (reason string, err error) {
	if err := util.MkdirConfined(o.Root, filepath.FromSlash(OffloadDir), util.SecureDirPerm); err != nil {
		return "", fmt.Errorf("mcp: create offload directory: %w", err)
	}
	if err := o.ensureSelfIgnore(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, offloadGitTimeout)
	defer cancel()
	inside, err := gitWorktreePresent(ctx, o.Root)
	if err != nil {
		return "", fmt.Errorf("mcp: look for a git work tree above the offload root: %w", err)
	}
	if !inside {
		return "", nil
	}
	probe := path.Join(OffloadDir, offloadProbeDigest+offloadExt)
	ignored, err := util.GitIgnoredPaths(ctx, o.Root, []string{probe}, true)
	switch {
	case err != nil:
		return "git could not be asked whether " + OffloadCacheDir + " is ignored", nil
	case !slices.Contains(ignored, probe):
		return "git does not ignore " + OffloadCacheDir, nil
	}
	return "", nil
}

// store writes text content-addressed and returns its pointer line. The directory and its
// ignore file exist already (prepare).
func (o Offloader) store(text string) (string, error) {
	if err := checkTextBound(len(text)); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(text))
	digest := hex.EncodeToString(sum[:])
	rel := filepath.Join(filepath.FromSlash(OffloadDir), digest+offloadExt)
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
	if err := o.recordWritten(digest); err != nil {
		return "", err
	}
	if err := o.prune(digest); err != nil {
		return "", err
	}
	return pointerLine(digest, text), nil
}

// ensureSelfIgnore writes a .gitignore holding "*" in the cache directory, so offloaded
// output never shows as untracked files in an adopter repository (the file ignores itself
// too). An existing file is left alone.
func (o Offloader) ensureSelfIgnore() error {
	rel := filepath.Join(filepath.FromSlash(OffloadCacheDir), ".gitignore")
	err := util.WriteFileConfinedExclusive(o.Root, rel, []byte("*\n"), util.SecureFilePerm)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mcp: write offload ignore file: %w", err)
	}
	return nil
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
	return fmt.Sprintf("[offloaded] path=%s bytes=%d sha256=%s read=%s head=%s",
		path.Join(OffloadDir, digest+offloadExt), len(text), digest, OffloadReadToolName, strconv.Quote(head))
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
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, 0, fmt.Errorf("%w: %s", ErrOffloadMissing, path.Join(OffloadDir, digest+offloadExt))
	}
	if err != nil {
		return "", 0, 0, fmt.Errorf("%w: %s", ErrOffloadUnreadable, path.Join(OffloadDir, digest+offloadExt))
	}
	if err := o.verifyStored(digest, data); err != nil {
		return "", 0, 0, err
	}
	total = len(data)
	if offset > total {
		return "", 0, total, fmt.Errorf("%w: offset %d beyond %d bytes", ErrOffloadRange, offset, total)
	}
	chunk, next = runeAligned(string(data), offset, min(offset+limit, total))
	return chunk, next, total, nil
}

// verifyStored refuses a stored file the server must not serve: one the server did not write
// (a committed or hand-placed file whose name is the digest of its content passes the digest
// check, so the record of written digests decides), one whose content does not match its
// digest, and one that is not a sanitizer fixed point. The last is what holds even when a
// record or file was forged: text the sanitizer would change was never stored by this server.
func (o Offloader) verifyStored(digest string, data []byte) error {
	name := path.Join(OffloadDir, digest+offloadExt)
	written, err := o.wasWritten(digest)
	if err != nil {
		return err
	}
	if !written {
		return fmt.Errorf("%w: %s was not written by this server", ErrOffloadUnreadable, name)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("%w: %s does not match its digest", ErrOffloadUnreadable, name)
	}
	safe, err := SanitizeText(string(data))
	if err != nil || safe != string(data) {
		return fmt.Errorf("%w: %s is not sanitized text", ErrOffloadUnreadable, name)
	}
	return nil
}

// runeAligned returns text[start:end] with both ends moved inward to rune boundaries, and
// the aligned end.
func runeAligned(text string, start, end int) (string, int) {
	start = runeStartFrom(text, start, end)
	end = runeStartBefore(text, start, end)
	if end == start && start < len(text) {
		// A limit smaller than one rune still advances by that rune.
		end = oneRuneEnd(text, start)
	}
	return text[start:end], end
}

// runeStartFrom moves start forward to the first rune boundary, never past end.
func runeStartFrom(text string, start, end int) int {
	for start < end && start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return start
}

// runeStartBefore moves end back to a rune boundary, never below start.
func runeStartBefore(text string, start, end int) int {
	for end < len(text) && end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	return end
}

// oneRuneEnd returns the end offset of the rune starting at start.
func oneRuneEnd(text string, start int) int {
	end := start + 1
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	return end
}
