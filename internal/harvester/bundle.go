package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxBundleEntries bounds every directory iteration in the bundler (HISS-02).
	MaxBundleEntries = 500
	// MaxSkillFiles bounds the number of files copied out of a single skill or plugin tree.
	MaxSkillFiles = 200
	// MaxSkillWalkDepth bounds how deep a skill or vault tree is descended.
	MaxSkillWalkDepth = 8
	// MaxCopyBuffer is the stream copy buffer size used for every bundled file.
	MaxCopyBuffer = 32 * 1024
	// MaxManifestRecords bounds the manifest record loops (HISS-02). Records accumulate
	// across every agent root, so this bound is per bundle, not per directory.
	MaxManifestRecords = 50000
	// MaxBundleManifestBytes bounds untrusted JSON before decoding.
	MaxBundleManifestBytes = 32 * 1024 * 1024

	// bundleDirPerm and bundleFilePerm keep the harvested material owner-only. The bundler
	// copies shell history, MCP configuration and agent state, all of which routinely carry
	// credentials, so nothing it writes may be group- or world-readable.
	bundleDirPerm  os.FileMode = 0o700
	bundleFilePerm os.FileMode = 0o600

	// skillRecordPrefix is the only manifest path prefix accepted for a "skill" record.
	skillRecordPrefix = "agent-skills/"
	// skillRecordSegments is the minimum number of path segments in a skill record:
	// agent-skills/<origin>/<name>/<file>.
	skillRecordSegments = 4
	// maxSkillRecordSegments bounds the segment validation loop (HISS-02) and with it the
	// depth a manifest record may reach inside a skill directory.
	maxSkillRecordSegments = skillRecordSegments + MaxSkillWalkDepth
)

var (
	// ErrNotRegularFile is returned when a bundle source is a symlink, device or directory
	// rather than a regular file.
	ErrNotRegularFile = errors.New("harvester: bundle source is not a regular file")
	// ErrBundleRecordPath is returned when a manifest record names a path that is absolute,
	// escapes its root, or is not under agent-skills/.
	ErrBundleRecordPath = errors.New("harvester: invalid bundle manifest record path")
	// ErrBundleIntegrity is returned when a bundle file does not match the size and SHA256
	// recorded for it in the manifest.
	ErrBundleIntegrity = errors.New("harvester: bundle file fails manifest integrity check")
)

// SensitiveBundleCategories lists the manifest categories whose contents routinely carry
// credentials (API keys, bearer tokens, exported secrets). Callers surface them so an
// operator knows what a bundle must be protected like before it is transferred.
var SensitiveBundleCategories = []string{
	"agent-config",
	"claude-config",
	"cli-history",
	"codex-config",
	"copilot-config",
	"hindsight-config",
}

// BundleOptions defines the parameters for harvesting a workstation state bundle.
type BundleOptions struct {
	WorkstationName string `json:"workstation_name"`
	OutputDir       string `json:"output_dir"`
	HomeDir         string `json:"home_dir"`
	DevDir          string `json:"dev_dir"`
	VaultDir        string `json:"vault_dir"`
	// IncludeShellHistory opts in to copying ~/.bash_history, ~/.zsh_history and the
	// PowerShell console history into the bundle. It defaults to false: shell history is
	// the canonical place where tokens are exported, and a bundle is a transferable
	// artefact.
	IncludeShellHistory bool `json:"include_shell_history,omitempty"`
	// Roots carries the per-OS client locations. The command layer resolves them through
	// internal/clientsetup, which this package cannot import (clientsetup reaches the
	// harvester through repairrun and dogfood), so they arrive as data.
	Roots ClientRoots `json:"roots"`
}

// ClientRoots holds the client locations that differ per operating system. An empty
// field means the location does not apply to the harvested host and is skipped.
type ClientRoots struct {
	// AGYConfig is the Antigravity global customization root.
	AGYConfig string `json:"agy_config,omitempty"`
	// AGYBrains lists the existing Antigravity brain directories, winner first.
	AGYBrains []string `json:"agy_brains,omitempty"`
	// ClaudeDesktopConfig is the Claude desktop configuration file.
	ClaudeDesktopConfig string `json:"claude_desktop_config,omitempty"`
	// PowerShellHistory is the PSReadLine console history file.
	PowerShellHistory string `json:"powershell_history,omitempty"`
}

// MaxBrainRoots bounds how many brain directories one bundle reads.
const MaxBrainRoots = 8

// BundleFileRecord records file metadata and integrity checksum.
type BundleFileRecord struct {
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Category     string `json:"category"`
}

// WorkstationBundleReport summarizes the harvested bundle contents.
type WorkstationBundleReport struct {
	WorkstationName string             `json:"workstation_name"`
	CapturedAt      time.Time          `json:"captured_at"`
	TotalFiles      int                `json:"total_files"`
	TotalBytes      int64              `json:"total_bytes"`
	Categories      map[string]int     `json:"categories"`
	ManifestPath    string             `json:"manifest_path"`
	Records         []BundleFileRecord `json:"records"`
	// Skipped records every source the bundler could not or would not copy, so that a
	// bundle is never silently incomplete.
	Skipped []string `json:"skipped,omitempty"`
}

// IngestReport summarizes comparison and ingestion analysis of a bundle against local workstation.
type IngestReport struct {
	WorkstationName string   `json:"workstation_name"`
	BundlePath      string   `json:"bundle_path"`
	NovelSkills     []string `json:"novel_skills"`
	ExistingSkills  []string `json:"existing_skills"`
	NovelMemories   []string `json:"novel_memories"`
	NovelPatches    []string `json:"novel_patches"`
	ValidIntegrity  bool     `json:"valid_integrity"`
	// RejectedRecords lists every manifest record refused because its path escaped the
	// skills root or its content did not match the recorded hash.
	RejectedRecords []string `json:"rejected_records,omitempty"`
	// Warnings preserves bounded source omissions from the captured bundle.
	Warnings []string `json:"warnings,omitempty"`
}

// bundleCollector accumulates the records and skip notes of a single bundle run.
type bundleCollector struct {
	baseDir         string
	records         []BundleFileRecord
	skipped         []string
	skippedOverflow int
}

// bundleFileSpec names a single source file and where it lands in the bundle.
type bundleFileSpec struct {
	src string
	dst string
	cat string
}

// note records a source the bundler did not copy. The list is bounded (HISS-02).
func (c *bundleCollector) note(format string, args ...any) {
	if len(c.skipped) >= MaxBundleEntries {
		c.skippedOverflow++
		c.skipped[MaxBundleEntries-1] = fmt.Sprintf("%d additional skipped sources omitted from bounded report", c.skippedOverflow+1)
		return
	}
	c.skipped = append(c.skipped, fmt.Sprintf(format, args...))
}

// copyOrNote copies one file into the bundle, recording a skip note instead of failing the
// whole run when a single source cannot be read.
func (c *bundleCollector) copyOrNote(ctx context.Context, src, dst, cat string) {
	if len(c.records) >= MaxManifestRecords {
		c.note("skip %s: manifest record limit %d reached", src, MaxManifestRecords)
		return
	}
	rec, err := copyFileWithHash(ctx, src, dst, cat, c.baseDir)
	if err != nil {
		c.note("skip %s: %v", src, err)
		return
	}
	c.records = append(c.records, *rec)
}

// readDir lists dir. A directory that does not exist simply means the agent is not
// installed; any other failure is recorded so that a permission error can never
// masquerade as "no files here".
func (c *bundleCollector) readDir(dir string) []os.DirEntry {
	// #nosec G304 -- dir comes from the operator-selected home/vault roots; this
	// read-only directory listing is bounded and never opens entry contents.
	file, err := os.Open(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.note("read directory %s: %v", dir, err)
		}
		return nil
	}
	entries, readErr := file.ReadDir(MaxBundleEntries + 1)
	if err := file.Close(); err != nil {
		c.note("close directory %s: %v", dir, err)
		return nil
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		c.note("read directory %s: %v", dir, readErr)
		return nil
	}
	if len(entries) > MaxBundleEntries {
		c.note("skip directory %s: more than %d entries", dir, MaxBundleEntries)
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries
}

// captureSpecs copies a fixed table of optional source files into the bundle.
func (c *bundleCollector) captureSpecs(ctx context.Context, specs []bundleFileSpec) error {
	for i := 0; i < len(specs) && i < MaxBundleEntries; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled while bundling agent state: %w", err)
		}
		if !util.FileExists(specs[i].src) {
			continue
		}
		c.copyOrNote(ctx, specs[i].src, specs[i].dst, specs[i].cat)
	}
	return nil
}

// openRegularSource opens src only when it is a regular file. A symlink planted inside a
// third-party skill or plugin directory must never redirect the bundler at an arbitrary
// file such as ~/.ssh/id_ed25519.
func openRegularSource(src string) (*os.File, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return nil, fmt.Errorf("inspect source file %s: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is %s", ErrNotRegularFile, src, info.Mode().Type())
	}

	// #nosec G304 -- src is built from the caller's own home/vault roots and has just been
	// Lstat-verified as a regular file; copying it is this function's entire purpose.
	file, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open source file %s: %w", src, err)
	}

	opened, sErr := file.Stat()
	if sErr != nil || !opened.Mode().IsRegular() {
		if cErr := file.Close(); cErr != nil {
			return nil, fmt.Errorf("close source file %s: %w", src, cErr)
		}
		if sErr != nil {
			return nil, fmt.Errorf("stat opened source file %s: %w", src, sErr)
		}
		return nil, fmt.Errorf("%w: %s changed type while opening", ErrNotRegularFile, src)
	}
	return file, nil
}

// rejectBundleLinks refuses target or its immediate parent being an existing symlink.
//
// This used to walk every component of the absolute path from the volume root down,
// Lstat-checking each and rejecting the first symlink. macOS ships /var and /tmp as symlinks
// to /private/var and /private/tmp, so any target under the platform's own TMPDIR --
// including every t.TempDir() fixture in this package's own tests -- was rejected at that
// walk before the caller's real check ever ran. That accounted for most of this package's
// failures on the macOS leg of the portability matrix (#135), the same shape #109 already
// fixed once: the operator's filesystem above where praetor reads or writes is not praetor's
// threat surface. What TestIngestTranscriptRejectsSourceAndDestinationLinkAncestors and
// TestIngestTranscriptRejectsCacheSymlinksAndConflicts actually plant is a symlink at target
// itself or at its immediate parent, so checking exactly those two -- both tolerant of a
// not-yet-created target -- keeps that coverage without walking the operator's own ancestry.
func rejectBundleLinks(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := rejectIfSymlink(absolute); err != nil {
		return err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return nil
	}
	return rejectIfSymlink(parent)
}

// rejectIfSymlink refuses path if it exists and is a symlink; a missing path is not an error,
// since rejectBundleLinks is also used to validate a destination that has not been created yet.
func rejectIfSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle output contains symlink: %s", path)
	}
	return nil
}

// tightenBundleMode preserves existing and umask restrictions, before any truncation.
func tightenBundleMode(file *os.File, expected os.FileInfo, ceiling os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if expected != nil && !os.SameFile(expected, info) {
		return fmt.Errorf("bundle destination changed while opening: %s", file.Name())
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("bundle destination is not regular: %s", file.Name())
	}
	mode := info.Mode().Perm() & ceiling
	if mode != info.Mode().Perm() {
		return file.Chmod(mode)
	}
	return nil
}

// secureBundleDirectory checks and tightens one directory through the pinned root.
func secureBundleDirectory(root *os.Root, relative string) error {
	if err := root.Mkdir(relative, bundleDirPerm); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("bundle destination is not a directory: %s", relative)
	}
	file, err := root.Open(relative)
	if err != nil {
		return err
	}
	return errors.Join(tightenBundleMode(file, info, bundleDirPerm), file.Close())
}

// openBundleRoot creates directory (through pinned, symlink-free parents) and pins it as a
// confinement root.
//
// This used to be rejectBundleLinks -- a walk from the volume root, Lstat-checking every
// existing component and rejecting the first symlink -- followed by a plain os.MkdirAll.
// macOS ships /var and /tmp as symlinks to /private/var and /private/tmp, so every bundle
// destination under the platform's own TMPDIR, including every t.TempDir() fixture in this
// package's own tests, failed at that walk before MkdirAll ever ran. That took down this
// package's whole test suite on the macOS leg of the portability matrix (#135), the same
// shape #109 already fixed once: the operator's filesystem above where praetor writes is not
// praetor's threat surface. contextopt.EnsureDirectory already carries that fix -- it resolves
// the existing ancestry once and rejects a symlink only at a component this call itself
// creates -- so this delegates instead of keeping a second implementation of the same walk
// (HISS-19).
func openBundleRoot(ctx context.Context, directory string) (*os.Root, error) {
	if err := contextopt.EnsureDirectory(ctx, directory, bundleDirPerm); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundle root is not a directory: %s", directory)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = fmt.Errorf("bundle root changed while opening: %s", directory)
	}
	if err == nil {
		err = secureBundleDirectory(root, ".")
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func createBundleParents(root *os.Root, relative string) error {
	parts := strings.Split(filepath.Dir(relative), string(os.PathSeparator))
	if len(parts) > MaxBundleEntries {
		return fmt.Errorf("bundle destination exceeds directory depth bound")
	}
	parent := "."
	for _, part := range parts {
		parent = filepath.Join(parent, part)
		if err := secureBundleDirectory(root, parent); err != nil {
			return err
		}
	}
	return nil
}

func openBundleFile(root *os.Root, relative string) (*os.File, error) {
	info, err := root.Lstat(relative)
	flags := os.O_WRONLY
	if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
		info = nil
	} else if err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("bundle destination is not a regular file: %s", relative)
	}
	file, err := root.OpenFile(relative, flags, bundleFilePerm)
	if err != nil {
		return nil, err
	}
	if err = tightenBundleMode(file, info, bundleFilePerm); err == nil {
		err = file.Truncate(0)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// createSecureDest pins the output root, rejects links, and opens before truncating.
func createSecureDest(ctx context.Context, dst, baseDir string) (*os.File, error) {
	relative, err := filepath.Rel(baseDir, dst)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil, fmt.Errorf("invalid bundle destination: %s", dst)
	}
	root, err := openBundleRoot(ctx, baseDir)
	if err != nil {
		return nil, err
	}
	if err := createBundleParents(root, relative); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	file, err := openBundleFile(root, relative)
	if closeErr := root.Close(); closeErr != nil {
		if file != nil {
			closeErr = errors.Join(closeErr, file.Close())
		}
		return nil, errors.Join(err, closeErr)
	}
	return file, err
}

// copyFileWithHash streams src into dst and returns the manifest record describing it. The
// destination close is checked: a filesystem that reports a write failure only at close
// must not leave the manifest asserting a hash for data that never reached the disk.
func copyFileWithHash(ctx context.Context, src, dst, category, baseDir string) (rec *BundleFileRecord, err error) {
	sFile, sErr := openRegularSource(src)
	if sErr != nil {
		return nil, sErr
	}
	defer func() {
		if cErr := sFile.Close(); cErr != nil && err == nil {
			rec, err = nil, fmt.Errorf("close source file %s: %w", src, cErr)
		}
	}()

	dFile, dErr := createSecureDest(ctx, dst, baseDir)
	if dErr != nil {
		return nil, dErr
	}
	defer func() {
		if cErr := dFile.Close(); cErr != nil && err == nil {
			rec, err = nil, fmt.Errorf("close bundle file %s: %w", dst, cErr)
		}
	}()

	hasher := sha256.New()
	written, cpErr := copyFileStream(ctx, io.MultiWriter(dFile, hasher), sFile)
	if cpErr != nil {
		return nil, fmt.Errorf("copy stream from %s to %s: %w", src, dst, cpErr)
	}

	rel, relErr := filepath.Rel(baseDir, dst)
	if relErr != nil {
		rel = filepath.Base(dst)
	}

	return &BundleFileRecord{
		RelativePath: filepath.ToSlash(rel),
		SizeBytes:    written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
		Category:     category,
	}, nil
}

// copyTree copies every regular file under srcDir into dstDir, preserving relative
// sub-paths so that a skill's references/ and scripts/ directories survive the round trip.
// Symlinks and other irregular entries are refused and recorded; both the file count and
// the directory depth are bounded (HISS-02).
func (c *bundleCollector) copyTree(ctx context.Context, srcDir, dstDir, cat string, maxFiles int) error {
	if !util.DirExists(srcDir) {
		return nil
	}

	files := 0
	walkErr := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			c.note("walk %s: %v", p, wErr)
			return fs.SkipDir
		}
		if cErr := ctx.Err(); cErr != nil {
			return cErr
		}
		rel, relErr := filepath.Rel(srcDir, p)
		if relErr != nil {
			c.note("relativize %s: %v", p, relErr)
			return fs.SkipDir
		}
		if d.IsDir() {
			if err := walkDepthGuard(rel); err != nil {
				c.note("skip directory %s: depth exceeds %d", p, MaxSkillWalkDepth)
				return err
			}
			return nil
		}
		if files >= maxFiles {
			c.note("truncated %s after %d files", srcDir, maxFiles)
			return fs.SkipAll
		}
		files++
		c.captureTreeEntry(ctx, p, filepath.Join(dstDir, rel), cat, d)
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("copy tree %s: %w", srcDir, walkErr)
	}
	return nil
}

// walkDepthGuard stops the tree walk beyond MaxSkillWalkDepth levels.
func walkDepthGuard(rel string) error {
	if rel == "." {
		return nil
	}
	if strings.Count(rel, string(os.PathSeparator))+1 > MaxSkillWalkDepth {
		return fs.SkipDir
	}
	return nil
}

// captureTreeEntry copies one walked entry, refusing anything that is not a regular file.
func (c *bundleCollector) captureTreeEntry(ctx context.Context, src, dst, cat string, d fs.DirEntry) {
	if d.Type()&os.ModeSymlink != 0 {
		c.note("skip symlink %s", src)
		return
	}
	if !d.Type().IsRegular() {
		c.note("skip irregular entry %s (%s)", src, d.Type())
		return
	}
	c.copyOrNote(ctx, src, dst, cat)
}

// bundleSkills copies every skill directory under srcDir, including its sub-directories.
func (c *bundleCollector) bundleSkills(ctx context.Context, srcDir, dstDir, cat string) error {
	entries := c.readDir(srcDir)

	for i := 0; i < len(entries) && i < MaxBundleEntries; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during skill bundle: %w", err)
		}
		entry := entries[i]
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillDir := filepath.Join(srcDir, entry.Name())
		if err := c.copyTree(ctx, skillDir, filepath.Join(dstDir, entry.Name()), cat, MaxSkillFiles); err != nil {
			return err
		}
	}
	return nil
}

// bundleClaudeMemories copies every project memory document under the Claude projects dir.
func (c *bundleCollector) bundleClaudeMemories(ctx context.Context, claudeProjectsDir, dstDir string) error {
	entries := c.readDir(claudeProjectsDir)

	for i := 0; i < len(entries) && i < MaxBundleEntries; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during memory bundle: %w", err)
		}
		entry := entries[i]
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		memDir := filepath.Join(claudeProjectsDir, entry.Name(), "memory")
		if err := c.copyFilesMatching(ctx, memDir, filepath.Join(dstDir, entry.Name()), ".md", "project-memory"); err != nil {
			return err
		}
	}
	return nil
}

// rootedPath joins below root. An empty root stays empty, so a location that does not
// apply to the host is skipped instead of resolving against the working directory.
func rootedPath(root string, segments ...string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(append([]string{root}, segments...)...)
}

// bundleBrains copies the transcripts of every existing brain directory into one
// destination. A host can hold several brains (IDE, CLI and the unsuffixed one), so
// each is read and the fact is recorded; on a duplicate conversation the first wins.
func (c *bundleCollector) bundleBrains(ctx context.Context, brainDirs []string, dstDir string) error {
	if len(brainDirs) > 1 {
		c.note("%d Antigravity brain directories exist; all are bundled: %s", len(brainDirs), strings.Join(brainDirs, ", "))
	}
	for i := 0; i < len(brainDirs) && i < MaxBrainRoots; i++ {
		if brainDirs[i] == "" {
			continue
		}
		if err := c.bundleTranscripts(ctx, brainDirs[i], dstDir); err != nil {
			return err
		}
	}
	if len(brainDirs) > MaxBrainRoots {
		c.note("skip %d brain directories: limit %d reached", len(brainDirs)-MaxBrainRoots, MaxBrainRoots)
	}
	return nil
}

// bundleTranscripts copies one transcript per agent conversation directory.
func (c *bundleCollector) bundleTranscripts(ctx context.Context, brainDir, dstDir string) error {
	entries := c.readDir(brainDir)

	for i := 0; i < len(entries) && i < MaxTranscriptsScan; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during transcript bundle: %w", err)
		}
		entry := entries[i]
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		tPath := filepath.Join(brainDir, entry.Name(), ".system_generated", "logs", "transcript.jsonl")
		if !util.FileExists(tPath) {
			continue
		}
		dstFile := filepath.Join(dstDir, fmt.Sprintf("%s.transcript.jsonl", entry.Name()))
		if util.FileExists(dstFile) {
			c.note("skip %s: conversation %s was already bundled from another brain directory", tPath, entry.Name())
			continue
		}
		c.copyOrNote(ctx, tPath, dstFile, "agent-transcript")
	}
	return nil
}

// bundleCLIHistory copies shell history. It is opt-in: shell history is the canonical
// place where credentials are exported, and a bundle is a transferable artefact.
func (c *bundleCollector) bundleCLIHistory(ctx context.Context, opts BundleOptions, dstDir string) error {
	include := opts.IncludeShellHistory
	histPaths := []string{
		filepath.Join(opts.HomeDir, ".bash_history"),
		filepath.Join(opts.HomeDir, ".zsh_history"),
	}
	if opts.Roots.PowerShellHistory != "" {
		histPaths = append([]string{opts.Roots.PowerShellHistory}, histPaths...)
	}

	if !include {
		for _, hp := range histPaths {
			if util.FileExists(hp) {
				c.note("skip shell history %s: pass IncludeShellHistory to bundle it", hp)
			}
		}
		return nil
	}

	specs := make([]bundleFileSpec, 0, len(histPaths))
	for _, hp := range histPaths {
		specs = append(specs, bundleFileSpec{src: hp, dst: filepath.Join(dstDir, filepath.Base(hp)), cat: "cli-history"})
	}
	return c.captureSpecs(ctx, specs)
}

// bundlePlugins copies each plugin manifest and the plugin's own skill tree.
func (c *bundleCollector) bundlePlugins(ctx context.Context, pluginsDir, dstDir string) error {
	entries := c.readDir(pluginsDir)

	for i := 0; i < len(entries) && i < MaxBundleEntries; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during plugin bundle: %w", err)
		}
		entry := entries[i]
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		pDir := filepath.Join(pluginsDir, entry.Name())
		pJSON := filepath.Join(pDir, "plugin.json")
		if util.FileExists(pJSON) {
			c.copyOrNote(ctx, pJSON, filepath.Join(dstDir, entry.Name(), "plugin.json"), "plugin-manifest")
		}
		skillsDst := filepath.Join(dstDir, entry.Name(), "skills")
		if err := c.bundleSkills(ctx, filepath.Join(pDir, "skills"), skillsDst, "plugin-skill"); err != nil {
			return err
		}
	}
	return nil
}

// bundleAgentConfigs copies the cross-agent configuration files.
func (c *bundleCollector) bundleAgentConfigs(ctx context.Context, opts BundleOptions, dstDir string) error {
	homeDir := opts.HomeDir
	sources := []bundleFileSpec{
		{rootedPath(opts.Roots.AGYConfig, "hooks.json"), "", "agent-config"},
		{rootedPath(opts.Roots.AGYConfig, "mcp_config.json"), "", "agent-config"},
		{filepath.Join(homeDir, ".claude", "CLAUDE.md"), "", "agent-rule"},
		{filepath.Join(homeDir, ".claude", "settings.json"), "", "agent-config"},
		{filepath.Join(homeDir, ".hindsight", "coding-agent.json"), "", "hindsight-config"},
		{filepath.Join(homeDir, ".hindsight", "cordana-hindsight-tunnel.ps1"), "", "hindsight-script"},
		{filepath.Join(homeDir, ".hindsight", "current-workspace.txt"), "", "hindsight-state"},
	}
	for i := range sources {
		sources[i].dst = filepath.Join(dstDir, filepath.Base(sources[i].src))
	}
	return c.captureSpecs(ctx, sources)
}

// copyFilesMatching copies the regular files directly inside dir whose name ends in ext.
func (c *bundleCollector) copyFilesMatching(ctx context.Context, dir, dstDir, ext, cat string) error {
	entries := c.readDir(dir)

	for i := 0; i < len(entries) && i < MaxBundleEntries; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled while copying %s: %w", dir, err)
		}
		entry := entries[i]
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if ext != "" && !strings.HasSuffix(entry.Name(), ext) {
			continue
		}
		c.copyOrNote(ctx, filepath.Join(dir, entry.Name()), filepath.Join(dstDir, entry.Name()), cat)
	}
	return nil
}

// bundleCodexState copies Codex memories, rollout summaries, configuration and rules.
func (c *bundleCollector) bundleCodexState(ctx context.Context, homeDir string) error {
	codexDir := filepath.Join(homeDir, ".codex")
	memDir := filepath.Join(codexDir, "memories")
	base := c.baseDir

	if err := c.copyFilesMatching(ctx, memDir, filepath.Join(base, "agent-memories", "codex"), ".md", "codex-memory"); err != nil {
		return err
	}
	rollouts := filepath.Join(memDir, "rollout_summaries")
	rolloutDst := filepath.Join(base, "agent-memories", "codex", "rollout_summaries")
	if err := c.copyFilesMatching(ctx, rollouts, rolloutDst, ".md", "codex-rollout"); err != nil {
		return err
	}

	cfg := filepath.Join(base, "agent-configs", "codex")
	rules := filepath.Join(base, "agent-rules", "codex")
	return c.captureSpecs(ctx, []bundleFileSpec{
		{filepath.Join(codexDir, "config.toml"), filepath.Join(cfg, "config.toml"), "codex-config"},
		{filepath.Join(codexDir, "hooks.json"), filepath.Join(cfg, "hooks.json"), "codex-config"},
		{filepath.Join(codexDir, "session_index.jsonl"), filepath.Join(cfg, "session_index.jsonl"), "codex-session-index"},
		{filepath.Join(codexDir, "models_cache.json"), filepath.Join(cfg, "models_cache.json"), "codex-config"},
		{filepath.Join(codexDir, "external_agent_session_imports.json"), filepath.Join(cfg, "external_agent_session_imports.json"), "codex-config"},
		{filepath.Join(codexDir, "AGENTS.md"), filepath.Join(rules, "AGENTS.md"), "codex-rule"},
		{filepath.Join(codexDir, "rules", "default.rules"), filepath.Join(rules, "default.rules"), "codex-rule"},
		{filepath.Join(codexDir, "browser", "config.toml"), filepath.Join(cfg, "browser.toml"), "codex-config"},
		{filepath.Join(codexDir, "computer-use", "config.toml"), filepath.Join(cfg, "computer-use.toml"), "codex-config"},
		{filepath.Join(codexDir, "vendor_imports", "skills-curated-cache.json"), filepath.Join(cfg, "skills-curated-cache.json"), "codex-config"},
	})
}

// bundleCodexPlugins copies the manifest of each cached Codex plugin.
func (c *bundleCollector) bundleCodexPlugins(ctx context.Context, homeDir string) error {
	cacheDir := filepath.Join(homeDir, ".codex", "plugins", "cache")
	types := []string{"openai-bundled", "openai-curated", "openai-curated-remote", "openai-primary-runtime"}

	for _, tName := range types {
		tDir := filepath.Join(cacheDir, tName)
		entries := c.readDir(tDir)
		for i := 0; i < len(entries) && i < MaxBundleEntries; i++ {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("context cancelled during codex plugin bundle: %w", err)
			}
			entry := entries[i]
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			srcManifest := filepath.Join(tDir, entry.Name(), ".codex-plugin", "plugin.json")
			if !util.FileExists(srcManifest) {
				continue
			}
			dst := filepath.Join(c.baseDir, "agent-plugins", "codex", tName, entry.Name(), "plugin.json")
			c.copyOrNote(ctx, srcManifest, dst, "codex-plugin-manifest")
		}
	}
	return nil
}

// bundleCopilotState copies Copilot configuration, hooks, logs and IDE locks.
func (c *bundleCollector) bundleCopilotState(ctx context.Context, homeDir string) error {
	copilotDir := filepath.Join(homeDir, ".copilot")
	cfg := filepath.Join(c.baseDir, "agent-configs", "copilot")

	err := c.captureSpecs(ctx, []bundleFileSpec{
		{filepath.Join(copilotDir, "config.json"), filepath.Join(cfg, "config.json"), "copilot-config"},
		{filepath.Join(copilotDir, "mcp-config.json"), filepath.Join(cfg, "mcp-config.json"), "copilot-config"},
		{filepath.Join(copilotDir, ".datacloud_skills_manifest"), filepath.Join(cfg, ".datacloud_skills_manifest"), "copilot-manifest"},
	})
	if err != nil {
		return err
	}

	if err := c.copyFilesMatching(ctx, filepath.Join(copilotDir, "hooks"), filepath.Join(cfg, "hooks"), "", "copilot-hook"); err != nil {
		return err
	}
	logsDst := filepath.Join(c.baseDir, "cli-logs", "copilot")
	if err := c.copyFilesMatching(ctx, filepath.Join(copilotDir, "logs"), logsDst, ".log", "copilot-log"); err != nil {
		return err
	}
	return c.copyFilesMatching(ctx, filepath.Join(copilotDir, "ide"), filepath.Join(cfg, "ide"), ".lock", "copilot-ide-lock")
}

// bundleClaudeAdditional copies the Claude configuration, rules and plugin registries.
func (c *bundleCollector) bundleClaudeAdditional(ctx context.Context, opts BundleOptions) error {
	homeDir := opts.HomeDir
	claudeDir := filepath.Join(homeDir, ".claude")
	cfg := filepath.Join(c.baseDir, "agent-configs", "claude")
	plugins := filepath.Join(c.baseDir, "agent-plugins", "claude")

	return c.captureSpecs(ctx, []bundleFileSpec{
		{filepath.Join(homeDir, ".claude.json"), filepath.Join(cfg, "claude.json"), "claude-config"},
		{filepath.Join(homeDir, ".claude.json.backup"), filepath.Join(cfg, "claude.json.backup"), "claude-config"},
		{opts.Roots.ClaudeDesktopConfig, filepath.Join(cfg, "claude_desktop_config.json"), "claude-desktop-config"},
		{filepath.Join(claudeDir, "settings.json"), filepath.Join(cfg, "settings.json"), "claude-config"},
		{filepath.Join(claudeDir, "CLAUDE.md"), filepath.Join(c.baseDir, "agent-rules", "claude", "CLAUDE.md"), "claude-rule"},
		{filepath.Join(claudeDir, "plugins", "installed_plugins.json"), filepath.Join(plugins, "installed_plugins.json"), "claude-plugin"},
		{filepath.Join(claudeDir, "plugins", "known_marketplaces.json"), filepath.Join(plugins, "known_marketplaces.json"), "claude-plugin"},
		{filepath.Join(claudeDir, "plugins", "blocklist.json"), filepath.Join(plugins, "blocklist.json"), "claude-plugin"},
	})
}

// harvestAllSkills copies every agent's skill and plugin tree into the bundle.
func (c *bundleCollector) harvestAllSkills(ctx context.Context, opts BundleOptions) error {
	base := c.baseDir
	homeDir := opts.HomeDir
	roots := []bundleFileSpec{
		{filepath.Join(homeDir, ".copilot", "skills"), filepath.Join(base, "agent-skills", "copilot"), "skill"},
		{filepath.Join(homeDir, ".codex", "skills"), filepath.Join(base, "agent-skills", "codex"), "skill"},
		{filepath.Join(homeDir, ".codex", "skills", ".system"), filepath.Join(base, "agent-skills", "codex-system"), "skill"},
		{filepath.Join(homeDir, ".claude", "skills"), filepath.Join(base, "agent-skills", "claude"), "skill"},
		{rootedPath(opts.Roots.AGYConfig, "skills"), filepath.Join(base, "agent-skills", "gemini"), "skill"},
		{filepath.Join(homeDir, ".agents", "skills"), filepath.Join(base, "agent-skills", "universal"), "skill"},
	}
	for _, r := range roots {
		if err := c.bundleSkills(ctx, r.src, r.dst, r.cat); err != nil {
			return fmt.Errorf("bundle skills from %s: %w", r.src, err)
		}
	}

	pluginRoots := []bundleFileSpec{
		{rootedPath(opts.Roots.AGYConfig, "plugins"), filepath.Join(base, "agent-plugins", "gemini"), ""},
		{filepath.Join(homeDir, ".agents", "plugins"), filepath.Join(base, "agent-plugins", "universal"), ""},
	}
	for _, r := range pluginRoots {
		if err := c.bundlePlugins(ctx, r.src, r.dst); err != nil {
			return fmt.Errorf("bundle plugins from %s: %w", r.src, err)
		}
	}
	return nil
}

// harvestAgentState copies the configuration, memory and transcript material of every agent.
func (c *bundleCollector) harvestAgentState(ctx context.Context, opts BundleOptions) error {
	base := c.baseDir
	home := opts.HomeDir

	steps := []func() error{
		func() error { return c.bundleAgentConfigs(ctx, opts, filepath.Join(base, "agent-configs")) },
		func() error { return c.bundleCodexState(ctx, home) },
		func() error { return c.bundleCodexPlugins(ctx, home) },
		func() error { return c.bundleCopilotState(ctx, home) },
		func() error { return c.bundleClaudeAdditional(ctx, opts) },
		func() error {
			memDst := filepath.Join(base, "agent-memories", "claude")
			return c.bundleClaudeMemories(ctx, filepath.Join(home, ".claude", "projects"), memDst)
		},
		func() error {
			return c.bundleBrains(ctx, opts.Roots.AGYBrains, filepath.Join(base, "agent-transcripts"))
		},
		func() error { return c.bundleCLIHistory(ctx, opts, filepath.Join(base, "cli-logs")) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return c.harvestVault(ctx, opts.VaultDir)
}

// harvestVault copies the optional workstation vault inventory and patches.
func (c *bundleCollector) harvestVault(ctx context.Context, vaultDir string) error {
	if vaultDir == "" {
		return nil
	}
	inventoryDst := filepath.Join(c.baseDir, "dev-inventory")
	if err := c.copyTree(ctx, filepath.Join(vaultDir, "dev-inventory"), inventoryDst, "inventory", MaxBundleEntries); err != nil {
		return err
	}
	patchesDst := filepath.Join(c.baseDir, "dev-patches")
	return c.copyTree(ctx, filepath.Join(vaultDir, "dev-patches"), patchesDst, "patch", MaxBundleEntries)
}

// writeBundleManifest totals the records and writes the owner-only manifest.
func writeBundleManifest(ctx context.Context, report *WorkstationBundleReport) (err error) {
	if len(report.Records) > MaxManifestRecords {
		return fmt.Errorf("bundle manifest exceeds %d records", MaxManifestRecords)
	}
	report.TotalFiles, report.TotalBytes = len(report.Records), 0
	report.Categories = make(map[string]int)
	for i := 0; i < len(report.Records) && i < MaxManifestRecords; i++ {
		report.TotalBytes += report.Records[i].SizeBytes
		report.Categories[report.Records[i].Category]++
	}

	manifestData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bundle manifest: %w", err)
	}
	file, err := createSecureDest(ctx, report.ManifestPath, filepath.Dir(report.ManifestPath))
	if err != nil {
		return fmt.Errorf("create bundle manifest: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(manifestData); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// BundleWorkstation captures skills, memories, transcripts, patches and CLI history into a
// bundle. Everything it writes is owner-only: the material routinely carries credentials.
func BundleWorkstation(ctx context.Context, opts BundleOptions) (*WorkstationBundleReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before workstation bundle: %w", err)
	}
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}

	baseDir := opts.OutputDir
	output, err := openBundleRoot(ctx, baseDir)
	if err != nil {
		return nil, fmt.Errorf("create bundle output directory %s: %w", baseDir, err)
	}
	if err := output.Close(); err != nil {
		return nil, err
	}

	collector := &bundleCollector{baseDir: baseDir, records: make([]BundleFileRecord, 0), skipped: make([]string, 0)}
	if opts.DevDir != "" {
		collector.note("development directory %s was not captured: development-repository capture is not implemented", opts.DevDir)
	}
	if err := collector.harvestAllSkills(ctx, opts); err != nil {
		return nil, err
	}
	if err := collector.harvestAgentState(ctx, opts); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before writing bundle manifest: %w", err)
	}

	report := &WorkstationBundleReport{
		WorkstationName: opts.WorkstationName,
		CapturedAt:      time.Now(),
		TotalFiles:      len(collector.records),
		Categories:      make(map[string]int),
		ManifestPath:    filepath.Join(baseDir, "manifest.json"),
		Records:         collector.records,
		Skipped:         collector.skipped,
	}
	if err := writeBundleManifest(ctx, report); err != nil {
		return nil, err
	}
	return report, nil
}

// skillRecordTarget is the validated source/destination pair of one ingested skill record.
type skillRecordTarget struct {
	name string
	src  string
	dst  string
}

// validateSkillRecordPath rejects a manifest path that is absolute, not normalised, not
// under agent-skills/, or that contains a ".." segment. A bundle is attacker-controlled
// input: without this the ingest would happily write outside the local skills directory.
func validateSkillRecordPath(relPath string) ([]string, error) {
	slashed := filepath.ToSlash(relPath)
	if err := checkSkillRecordShape(relPath, slashed); err != nil {
		return nil, err
	}

	parts := strings.Split(slashed, "/")
	if len(parts) < skillRecordSegments {
		return nil, fmt.Errorf("%w: %q has too few segments", ErrBundleRecordPath, relPath)
	}
	if len(parts) > maxSkillRecordSegments {
		return nil, fmt.Errorf("%w: %q has more than %d segments", ErrBundleRecordPath, relPath, maxSkillRecordSegments)
	}
	if err := checkSkillRecordSegments(relPath, parts); err != nil {
		return nil, err
	}
	return parts, nil
}

// checkSkillRecordShape rejects a record path that is absolute, unnormalised, or outside
// the agent-skills/ prefix.
func checkSkillRecordShape(relPath, slashed string) error {
	if slashed == "" || filepath.IsAbs(relPath) || strings.HasPrefix(slashed, "/") {
		return fmt.Errorf("%w: %q is not a relative path", ErrBundleRecordPath, relPath)
	}
	if path.Clean(slashed) != slashed {
		return fmt.Errorf("%w: %q is not normalised", ErrBundleRecordPath, relPath)
	}
	if !strings.HasPrefix(slashed, skillRecordPrefix) {
		return fmt.Errorf("%w: %q is not under %s", ErrBundleRecordPath, relPath, skillRecordPrefix)
	}
	return nil
}

// checkSkillRecordSegments rejects empty, traversing or hidden path segments.
func checkSkillRecordSegments(relPath string, parts []string) error {
	for i := 0; i < len(parts) && i < maxSkillRecordSegments; i++ {
		if parts[i] == "" || parts[i] == "." || parts[i] == ".." {
			return fmt.Errorf("%w: %q contains an empty or traversing segment", ErrBundleRecordPath, relPath)
		}
	}
	if strings.HasPrefix(parts[2], ".") {
		return fmt.Errorf("%w: %q names a hidden skill", ErrBundleRecordPath, relPath)
	}
	return nil
}

// resolveSkillRecord confines the record's source inside the bundle and its destination
// inside the local skills directory.
func resolveSkillRecord(r BundleFileRecord, bundleDir, localSkillsDir string) (*skillRecordTarget, error) {
	parts, err := validateSkillRecordPath(r.RelativePath)
	if err != nil {
		return nil, err
	}

	src, srcErr := util.ConfinePath(bundleDir, filepath.FromSlash(r.RelativePath))
	if srcErr != nil {
		return nil, fmt.Errorf("confine bundle source %q: %w", r.RelativePath, srcErr)
	}
	name := parts[2]
	member := filepath.Join(append([]string{name}, parts[skillRecordSegments-1:]...)...)
	dst, dstErr := util.ConfinePath(localSkillsDir, member)
	if dstErr != nil {
		return nil, fmt.Errorf("confine skill destination %q: %w", member, dstErr)
	}
	return &skillRecordTarget{name: name, src: src, dst: dst}, nil
}

// hashFile returns the hex SHA256 and byte size of a regular file.
func hashFile(ctx context.Context, target string) (sum string, size int64, err error) {
	file, openErr := openRegularSource(target)
	if openErr != nil {
		return "", 0, openErr
	}
	defer func() {
		if cErr := file.Close(); cErr != nil && err == nil {
			sum, size, err = "", 0, fmt.Errorf("close %s: %w", target, cErr)
		}
	}()

	hasher := sha256.New()
	size, err = copyFileStream(ctx, hasher, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", target, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// verifyRecordIntegrity recomputes the digest of a bundle file and compares it with the
// size and SHA256 the manifest recorded for it.
func verifyRecordIntegrity(ctx context.Context, src string, r BundleFileRecord) error {
	if r.SHA256 == "" {
		return fmt.Errorf("%w: %s carries no sha256 in the manifest", ErrBundleIntegrity, r.RelativePath)
	}
	sum, size, err := hashFile(ctx, src)
	if err != nil {
		return err
	}
	if size != r.SizeBytes {
		return fmt.Errorf("%w: %s is %d bytes, manifest says %d", ErrBundleIntegrity, r.RelativePath, size, r.SizeBytes)
	}
	if !strings.EqualFold(sum, r.SHA256) {
		return fmt.Errorf("%w: %s hashes to %s, manifest says %s", ErrBundleIntegrity, r.RelativePath, sum, r.SHA256)
	}
	return nil
}

// copyFileSimple copies src to dst with owner-only permissions.
func copyFileSimple(ctx context.Context, src, dst, baseDir string) (err error) {
	sFile, sErr := openRegularSource(src)
	if sErr != nil {
		return sErr
	}
	defer func() {
		if cErr := sFile.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close source file %s: %w", src, cErr)
		}
	}()

	dFile, dErr := createSecureDest(ctx, dst, baseDir)
	if dErr != nil {
		return dErr
	}
	defer func() {
		if cErr := dFile.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close ingested file %s: %w", dst, cErr)
		}
	}()

	if _, cpErr := copyFileStream(ctx, dFile, sFile); cpErr != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, cpErr)
	}
	return nil
}

// ingestState carries the mutable bookkeeping of a single IngestBundle run.
type ingestState struct {
	bundleDir      string
	localSkillsDir string
	dryRun         bool
	report         *IngestReport
	seenNovel      map[string]bool
	seenExisting   map[string]bool
	seenMemory     map[string]bool
	seenPatch      map[string]bool
}

// reject records a refused manifest record and clears the integrity flag.
func (s *ingestState) reject(format string, args ...any) {
	s.report.ValidIntegrity = false
	if len(s.report.RejectedRecords) >= MaxBundleEntries {
		return
	}
	s.report.RejectedRecords = append(s.report.RejectedRecords, fmt.Sprintf(format, args...))
}

// installSkillFile verifies and copies one skill file into the local skills directory.
func (s *ingestState) installSkillFile(ctx context.Context, r BundleFileRecord, target *skillRecordTarget) {
	if err := verifyRecordIntegrity(ctx, target.src, r); err != nil {
		s.reject("%v", err)
		return
	}
	if s.dryRun {
		return
	}
	if err := copyFileSimple(ctx, target.src, target.dst, s.localSkillsDir); err != nil {
		s.reject("copy %s: %v", r.RelativePath, err)
	}
}

// processSkillRecord classifies a skill record as novel or already present and, for a
// novel skill outside dry-run mode, installs the file.
func (s *ingestState) processSkillRecord(ctx context.Context, r BundleFileRecord) {
	target, err := resolveSkillRecord(r, s.bundleDir, s.localSkillsDir)
	if err != nil {
		s.reject("%v", err)
		return
	}
	if s.seenExisting[target.name] {
		return
	}
	if !s.seenNovel[target.name] {
		if util.PathExists(filepath.Join(s.localSkillsDir, target.name)) {
			s.seenExisting[target.name] = true
			s.report.ExistingSkills = append(s.report.ExistingSkills, target.name)
			return
		}
		s.seenNovel[target.name] = true
		s.report.NovelSkills = append(s.report.NovelSkills, target.name)
	}
	s.installSkillFile(ctx, r, target)
}

// processRecord dispatches one manifest record by category.
func (s *ingestState) processRecord(ctx context.Context, r BundleFileRecord) {
	if err := verifyDeclaredRecord(ctx, s.bundleDir, r); err != nil {
		s.reject("%v", err)
		return
	}
	switch r.Category {
	case "skill":
		s.processSkillRecord(ctx, r)
	case "project-memory":
		if !s.seenMemory[r.RelativePath] {
			s.seenMemory[r.RelativePath] = true
			s.report.NovelMemories = append(s.report.NovelMemories, r.RelativePath)
		}
	case "patch":
		if !s.seenPatch[r.RelativePath] {
			s.seenPatch[r.RelativePath] = true
			s.report.NovelPatches = append(s.report.NovelPatches, r.RelativePath)
		}
	}
}

// verifyDeclaredRecord validates every asset, even when ingestion will not install it.
func verifyDeclaredRecord(ctx context.Context, bundleDir string, record BundleFileRecord) error {
	relative := record.RelativePath
	if !filepath.IsLocal(relative) || path.Clean(relative) != relative || relative == "." || strings.Contains(relative, "\\") {
		return fmt.Errorf("%w: %q is not a normalized relative path", ErrBundleRecordPath, relative)
	}
	source, err := util.ConfinePath(bundleDir, filepath.FromSlash(relative))
	if err != nil {
		return fmt.Errorf("confine bundle asset %q: %w", relative, err)
	}
	return verifyRecordIntegrity(ctx, source, record)
}

// readBundleManifest bounds and decodes a regular, confined manifest file.
func readBundleManifest(bundleDir string) (report *WorkstationBundleReport, err error) {
	manifestPath, err := util.ConfinePath(bundleDir, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("resolve bundle manifest: %w", err)
	}
	file, err := openRegularSource(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open bundle manifest: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			report, err = nil, errors.Join(err, closeErr)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, MaxBundleManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bundle manifest: %w", err)
	}
	if len(data) > MaxBundleManifestBytes {
		return nil, fmt.Errorf("bundle manifest exceeds %d bytes", MaxBundleManifestBytes)
	}
	var decoded WorkstationBundleReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("unmarshal bundle manifest: %w", err)
	}
	return &decoded, nil
}

// IngestBundle parses a bundle manifest and cross-references against local workstation
// skills. Every record is confined to the bundle and to the local skills directory, and
// every file is verified against the size and SHA256 the manifest recorded for it before
// it is copied; ValidIntegrity reports the outcome of those checks.
func IngestBundle(ctx context.Context, bundleDir, localSkillsDir string, dryRun bool) (*IngestReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before ingest: %w", err)
	}

	rep, err := readBundleManifest(bundleDir)
	if err != nil {
		return nil, err
	}

	state := &ingestState{
		bundleDir:      bundleDir,
		localSkillsDir: localSkillsDir,
		dryRun:         dryRun,
		report: &IngestReport{
			WorkstationName: rep.WorkstationName,
			BundlePath:      bundleDir,
			NovelSkills:     make([]string, 0),
			ExistingSkills:  make([]string, 0),
			NovelMemories:   make([]string, 0),
			NovelPatches:    make([]string, 0),
			ValidIntegrity:  true,
			Warnings:        append([]string(nil), rep.Skipped[:min(len(rep.Skipped), MaxBundleEntries)]...),
			RejectedRecords: make([]string, 0),
		},
		seenNovel:    make(map[string]bool),
		seenExisting: make(map[string]bool),
		seenMemory:   make(map[string]bool),
		seenPatch:    make(map[string]bool),
	}

	if len(rep.Records) > MaxManifestRecords {
		state.reject("manifest declares %d records, only the first %d are ingested",
			len(rep.Records), MaxManifestRecords)
	}
	for i := 0; i < len(rep.Records) && i < MaxManifestRecords; i++ {
		if cErr := ctx.Err(); cErr != nil {
			return nil, fmt.Errorf("context cancelled during ingest: %w", cErr)
		}
		state.processRecord(ctx, rep.Records[i])
		if err := ctx.Err(); err != nil {
			return state.report, fmt.Errorf("context cancelled during ingest: %w", err)
		}
	}

	sort.Strings(state.report.NovelSkills)
	sort.Strings(state.report.ExistingSkills)
	sort.Strings(state.report.NovelMemories)
	sort.Strings(state.report.NovelPatches)
	return state.report, nil
}

// bundleContextReader checks cancellation between bounded reads, including hashing.
// Regular-file system calls themselves retain the operating system's I/O semantics.
type bundleContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r bundleContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}

// copyFileStream bounds work by the opened file size and propagates cancellation.
// A file growing during capture is rejected rather than extending the copy forever.
func copyFileStream(ctx context.Context, destination io.Writer, source *os.File) (int64, error) {
	info, err := source.Stat()
	if err != nil {
		return 0, err
	}
	const maxStreamBytes int64 = 1<<63 - 2
	if info.Size() < 0 || info.Size() > maxStreamBytes {
		return 0, fmt.Errorf("invalid bundle source size: %d", info.Size())
	}
	reader := bundleContextReader{ctx: ctx, source: io.LimitReader(source, info.Size()+1)}
	written, err := io.CopyBuffer(destination, reader, make([]byte, MaxCopyBuffer))
	if err != nil {
		return written, err
	}
	if err := ctx.Err(); err != nil {
		return written, err
	}
	if written > info.Size() {
		return written, fmt.Errorf("bundle source grew during capture: %s", source.Name())
	}
	return written, nil
}
