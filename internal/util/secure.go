package util

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SecureFilePerm is the default file mode applied by WriteFileSecure when the
	// caller passes a zero perm: readable and writable by the owner only.
	SecureFilePerm os.FileMode = 0o600
	// SecureDirPerm is the default directory mode applied by MkdirSecure when the
	// caller passes a zero perm: accessible by the owner only.
	SecureDirPerm os.FileMode = 0o700
	// ownerWriteBit is the permission bit whose absence marks an existing file as
	// write-protected for the no-follow writers.
	ownerWriteBit os.FileMode = 0o200
	// MaxExecArgLen bounds the length of a single externally supplied exec argument.
	MaxExecArgLen = 4096
	// maxPathAncestorWalk bounds the ancestor walk in ConfinePath (HISS-02: every loop
	// carries a scalar upper bound).
	maxPathAncestorWalk = 256
	// maxStageAttempts bounds how often createStage retries a colliding temp file name.
	maxStageAttempts = 8
	// stageSuffixLen is the number of random base32 characters in a temp file name.
	stageSuffixLen = 12
	// execArgMetaChars lists the shell metacharacters rejected by ValidateExecArg.
	execArgMetaChars = ";&|$`<>(){}\"'\\"
)

var (
	// ErrEmptyRoot is returned when ConfinePath is given an empty confinement root.
	ErrEmptyRoot = errors.New("util: confinement root must not be empty")
	// ErrAbsoluteRelPath is returned when ConfinePath is given an absolute member path.
	ErrAbsoluteRelPath = errors.New("util: member path must be relative to the root")
	// ErrPathEscapesRoot is returned when a member path resolves outside its root, whether
	// ConfinePath's check catches it or a pinned os.Root refuses it at use time.
	ErrPathEscapesRoot = errors.New("util: path escapes the confinement root")
	// ErrRootItself is returned by WriteFileConfined for a member path that names the
	// confinement root itself instead of a file below it.
	ErrRootItself = errors.New("util: member path names the confinement root itself")
	// ErrInsecurePerm is returned for permission bits that are world-writable or that
	// carry non-permission mode bits (setuid, setgid, sticky).
	ErrInsecurePerm = errors.New("util: insecure permission bits")
	// ErrEmptyExecArg is returned when ValidateExecArg receives an empty argument.
	ErrEmptyExecArg = errors.New("util: exec argument must not be empty")
	// ErrExecArgOption is returned when an exec argument would be parsed as an option.
	ErrExecArgOption = errors.New("util: exec argument must not start with '-'")
	// ErrExecArgMeta is returned when an exec argument contains a shell metacharacter
	// or a control character.
	ErrExecArgMeta = errors.New("util: exec argument contains a forbidden character")
	// ErrExecArgTooLong is returned when an exec argument exceeds MaxExecArgLen.
	ErrExecArgTooLong = errors.New("util: exec argument exceeds the maximum length")
	// ErrFileTooLarge is returned by ReadConfinedLimited when the file carries more
	// than the caller's limit.
	ErrFileTooLarge = errors.New("util: file exceeds the read limit")
	// ErrInvalidReadLimit is returned by ReadConfinedLimited for a non-positive limit.
	ErrInvalidReadLimit = errors.New("util: read limit must be positive")
)

// ReadConfined reads rel below root after confining it with ConfinePath, so a
// caller-supplied repository path or a workspace glob can never read outside
// root. It is the read half of the confinement contract: callers that only ever
// read a file below a root should reach for this rather than pairing ConfinePath
// with their own os.ReadFile.
func ReadConfined(root, rel string) ([]byte, error) {
	path, err := ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is confined to root by ConfinePath above.
	return os.ReadFile(path)
}

// ReadConfinedLimited reads rel below root like ReadConfined, but never allocates more
// than limit+1 bytes: a file that carries more is refused with ErrFileTooLarge instead of
// being read to find out how big it is (HISS-02).
//
// This is the read to reach for whenever the size of the file is not the caller's to
// choose -- a configuration path inside an audited repository, a manifest inside an
// adopted checkout. ReadConfined confines the path but hands the whole file to os.ReadFile,
// so a multi-GB blob sitting at a configuration path is allocated in full before any
// caller-side length check can reject it, which on a memory-capped runner is an OOM rather
// than a finding.
func ReadConfinedLimited(root, rel string, limit int64) (data []byte, resultErr error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidReadLimit, limit)
	}
	path, err := ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is confined to root by ConfinePath above.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("util: read %q: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %q carries more than %d bytes", ErrFileTooLarge, path, limit)
	}
	return data, nil
}

// ConfinePath joins rel onto root and returns the cleaned, absolute result, guaranteeing
// that the result cannot leave root either lexically (via "..") or through a symbolic
// link. Paths that do not exist yet are still confined: the check resolves the deepest
// existing ancestor and re-attaches the remaining segments. Both the resolved parent
// directory and, when the final element exists, its resolved target must lie inside the
// resolved root: a create or rename lands in the parent, so a final element that links
// back into the root cannot vouch for a parent directory that lives outside it (BUG-826).
// A root of "/" (or a Windows volume root) confines every path on that volume (BUG-825).
//
// The result is the lexical join, never a resolved location. Callers relate results to
// each other and to root (privatePlanningOutput in cmd/standards-mcp takes the Rel of two
// of them), and a no-follow caller must still see a link at the final element. A resolved
// string would not close the window between this check and the caller's own open anyway:
// any in-root directory can be swapped for a link after the check just as a link can be
// retargeted. A writer that must stay confined through that window uses WriteFileConfined
// and MkdirConfined, which resolve every component through a pinned os.Root at use time.
//
// gosec: this is the canonical sanitizer for G304 (file inclusion via variable) and
// G305 (file traversal when extracting an archive). Pass user-, config- or
// archive-supplied relative paths through ConfinePath before handing them to os.Open,
// os.ReadFile, WriteFileSecure or MkdirSecure.
func ConfinePath(root, rel string) (string, error) {
	_, candidate, err := confine(root, rel)
	if err != nil {
		return "", err
	}
	return candidate, nil
}

// confine is ConfinePath's check. It returns the absolute root and the cleaned lexical
// join once both have passed the lexical and the resolved confinement checks.
func confine(root, rel string) (absRoot, candidate string, err error) {
	absRoot, candidate, err = lexicalConfine(root, rel)
	if err != nil {
		return "", "", err
	}
	resolvedRoot, err := resolveExistingAncestor(absRoot)
	if err != nil {
		return "", "", err
	}
	if candidate != absRoot {
		if err := checkResolvedConfinement(candidate, resolvedRoot); err != nil {
			return "", "", err
		}
	}
	return absRoot, candidate, nil
}

// confineBelow runs ConfinePath's check and returns the absolute root together with rel's
// cleaned position inside it ("." for the root itself), the form os.Root methods take.
func confineBelow(root, rel string) (absRoot, inside string, err error) {
	absRoot, candidate, err := confine(root, rel)
	if err != nil {
		return "", "", err
	}
	inside, err = filepath.Rel(absRoot, candidate)
	if err != nil {
		return "", "", fmt.Errorf("util: relate %q to the confinement root %q: %w", candidate, absRoot, err)
	}
	return absRoot, inside, nil
}

// lexicalConfine validates root and rel and returns the absolute root together with the
// cleaned join, refusing a join that leaves the root through "..".
func lexicalConfine(root, rel string) (absRoot, candidate string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", ErrEmptyRoot
	}
	if filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("%w: %q", ErrAbsoluteRelPath, rel)
	}
	absRoot, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", "", fmt.Errorf("util: resolve confinement root %q: %w", root, err)
	}
	candidate = filepath.Clean(filepath.Join(absRoot, rel))
	if !withinRoot(absRoot, candidate) {
		return "", "", fmt.Errorf("%w: %q is not under %q", ErrPathEscapesRoot, candidate, absRoot)
	}
	return absRoot, candidate, nil
}

// checkResolvedConfinement resolves candidate's parent directory and candidate itself and
// requires both to stay inside resolvedRoot.
func checkResolvedConfinement(candidate, resolvedRoot string) error {
	parent, err := resolveExistingAncestor(filepath.Dir(candidate))
	if err != nil {
		return err
	}
	target, err := resolveExistingAncestor(candidate)
	if err != nil {
		return err
	}
	for _, resolved := range [...]string{parent, target} {
		if !withinRoot(resolvedRoot, resolved) {
			return fmt.Errorf("%w: %q resolves to %q, outside %q",
				ErrPathEscapesRoot, candidate, resolved, resolvedRoot)
		}
	}
	return nil
}

// withinRoot reports whether p is root itself or a descendant of root. Both arguments
// must already be cleaned absolute paths. A root that already ends in a separator -- "/"
// or a Windows volume root such as `C:\` -- is its own prefix; appending another separator
// would demand a doubled one that no cleaned path carries (BUG-825).
func withinRoot(root, p string) bool {
	if p == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(p, prefix)
}

// resolveExistingAncestor resolves symlinks in the deepest existing ancestor of path and
// re-attaches the not-yet-existing trailing segments.
func resolveExistingAncestor(path string) (string, error) {
	current := path
	rest := ""
	for i := 0; i < maxPathAncestorWalk; i++ {
		if _, err := os.Lstat(current); err == nil {
			resolved, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil {
				return "", fmt.Errorf("util: resolve symlinks for %q: %w", current, evalErr)
			}
			if rest == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, rest), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("util: inspect %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("util: no existing ancestor for %q", path)
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
	return "", fmt.Errorf("util: %q exceeds the %d level ancestor walk bound", path, maxPathAncestorWalk)
}

// WriteFileSecure creates or truncates path and writes data. perm is a permission
// ceiling: existing permission bits and a restrictive creation umask are never widened.
// Required permission tightening happens before truncation, so metadata failures leave
// existing contents intact. World-writable and non-permission requests are refused.
//
// gosec: addresses G306 (WriteFile with permissions above 0600) and G302 (OpenFile with
// permissive mode). Call sites pass an explicit perm; perm == 0 selects SecureFilePerm.
func WriteFileSecure(path string, data []byte, perm os.FileMode) (err error) {
	perm, err = effectivePerm(perm, SecureFilePerm)
	if err != nil {
		return err
	}

	// #nosec G304 -- callers validate paths with ConfinePath; permissions are checked above.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return fmt.Errorf("util: open %q for writing: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("util: close %q: %w", path, cerr)
		}
	}()

	if err := tightenFilePermissions(file, perm); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("util: truncate %q: %w", path, err)
	}
	if _, werr := file.Write(data); werr != nil {
		return fmt.Errorf("util: write %q: %w", path, werr)
	}
	return nil
}

func tightenFilePermissions(file *os.File, perm os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("util: stat %q: %w", file.Name(), err)
	}
	return tightenMode(file.Name(), info, perm, file.Chmod)
}

// tightenMode is the one permission-ceiling decision behind WriteFileSecure, MkdirSecure
// and MkdirConfined: it clears every bit of info's mode that perm does not grant, through
// chmod, and never sets a bit. name only labels the error.
func tightenMode(name string, info os.FileInfo, perm os.FileMode, chmod func(os.FileMode) error) error {
	target := info.Mode().Perm() & perm
	if target == info.Mode().Perm() {
		return nil
	}
	if err := chmod(target); err != nil {
		return fmt.Errorf("util: chmod %q to %#o: %w", name, target, err)
	}
	return nil
}

// WriteFileAtomic writes data so a reader can never observe a torn or truncated file at
// path: it writes to a sibling temporary file in path's own directory (so the rename
// below stays on one filesystem and is therefore atomic) and renames that file onto path
// only once its write and its fsync have both succeeded. A failure anywhere before the
// rename leaves path exactly as it was and removes the temp file; it never truncates the
// target. WriteFileSecure truncates path in place before writing, so a process that dies
// between the truncate and the write leaves a zero-length or partially written file
// (BUG-447) -- use WriteFileAtomic instead wherever a reader may run concurrently with a
// writer, or a partial write would otherwise destroy the previous, valid contents.
//
// path's directory is opened once and pinned (os.Root): the temp file, its fsync and the
// rename all resolve against that handle, so an ancestor swapped for a symbolic link
// mid-write cannot move where the file lands. The rename replaces a symbolic link at path
// instead of writing through it. Like every rename-based writer it needs write permission
// on the directory, and the replacement is a new inode: hard links to the old file keep
// the old contents, and its owner and extended attributes do not carry forward.
//
// perm is the same permission ceiling WriteFileSecure enforces: a zero perm selects
// SecureFilePerm, and world-writable or non-permission bits are refused. Unlike
// WriteFileSecure, perm is not intersected with any pre-existing file at path: the
// rename replaces that file outright, so its historical permission bits do not carry
// forward, matching every other atomic-rename writer. WriteFileNoFollow is the variant
// that refuses a symbolic-link destination and keeps WriteFileSecure's ceiling.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureFilePerm)
	if err != nil {
		return err
	}
	return inParentDirectory(path, func(dir *os.Root, name string) error {
		return replaceAtomically(dir, name, data, filePermission{mode: perm, exact: true})
	})
}

// filePermission is the mode replaceAtomically gives the staged file before the rename.
// exact sets mode outright; otherwise mode is a ceiling that only tightens the creation
// mode, which already carries the process umask.
type filePermission struct {
	mode  os.FileMode
	exact bool
}

func (p filePermission) apply(file *os.File) error {
	if !p.exact {
		return tightenFilePermissions(file, p.mode)
	}
	if err := file.Chmod(p.mode); err != nil {
		return fmt.Errorf("util: chmod temp file %q to %#o: %w", file.Name(), p.mode, err)
	}
	return nil
}

// inParentDirectory opens path's directory as a pinned os.Root, runs fn with that handle
// and path's final element, and closes the handle.
func inParentDirectory(path string, fn func(dir *os.Root, name string) error) error {
	clean := filepath.Clean(path)
	return inRoot(filepath.Dir(clean), ".", func(dir *os.Root) error {
		return fn(dir, filepath.Base(clean))
	})
}

// inRoot opens dir as a pinned os.Root, opens sub below it through that handle ("." is
// dir itself), runs fn with the handle on sub, and closes every handle it opened. Opening
// sub through the handle follows a link only while it stays inside dir, so a component of
// sub swapped for an escaping link at any point before the open is refused, not followed.
func inRoot(dir, sub string, fn func(pinned *os.Root) error) (err error) {
	base, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("util: open directory %q: %w", dir, err)
	}
	defer closePinned(base, &err)
	if sub == "." {
		return fn(base)
	}
	pinned, err := base.OpenRoot(sub)
	if err != nil {
		return fmt.Errorf("util: open directory %q below %q: %w", sub, dir, err)
	}
	defer closePinned(pinned, &err)
	return fn(pinned)
}

// closePinned closes a handle opened by inRoot and reports a close failure through err
// unless an earlier error is already being returned.
func closePinned(pinned *os.Root, err *error) {
	if cerr := pinned.Close(); cerr != nil && *err == nil {
		*err = fmt.Errorf("util: close directory %q: %w", pinned.Name(), cerr)
	}
}

// replaceAtomically is the one atomic-replace implementation behind WriteFileAtomic and
// WriteFileNoFollow. It stages data in a fresh sibling of name inside dir, applies perm,
// fsyncs, and renames the stage onto name. Every failure before the rename removes the
// stage and leaves name untouched.
func replaceAtomically(dir *os.Root, name string, data []byte, perm filePermission) error {
	stage, tmp, err := createStage(dir, name, perm.mode)
	if err != nil {
		return err
	}
	if writeErr := writeAndSyncTemp(tmp, data, perm); writeErr != nil {
		// #nosec G104 -- best-effort; a leftover temp file self-heals on the next write attempt.
		dir.Remove(stage) //nolint:errcheck // best-effort; a leftover temp file self-heals on the next write attempt and must not mask writeErr
		return writeErr
	}
	if renameErr := dir.Rename(stage, name); renameErr != nil {
		// #nosec G104 -- best-effort; a leftover temp file self-heals on the next write attempt.
		dir.Remove(stage) //nolint:errcheck // best-effort; a leftover temp file self-heals on the next write attempt and must not mask renameErr
		return fmt.Errorf("util: rename %q to %q in %q: %w", stage, name, dir.Name(), renameErr)
	}
	return nil
}

// createStage exclusively creates a temporary file next to name. The random suffix makes
// a collision vanishingly unlikely, O_EXCL makes one harmless, and the retry is bounded
// (HISS-02).
func createStage(dir *os.Root, name string, perm os.FileMode) (string, *os.File, error) {
	var lastErr error
	for attempt := 0; attempt < maxStageAttempts; attempt++ {
		stage := ".tmp-" + name + "-" + rand.Text()[:stageSuffixLen]
		file, err := dir.OpenFile(stage, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return stage, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("util: create temp file for %q in %q: %w", name, dir.Name(), err)
		}
		lastErr = err
	}
	return "", nil, fmt.Errorf("util: create temp file for %q in %q: %d attempts collided: %w",
		name, dir.Name(), maxStageAttempts, lastErr)
}

// writeAndSyncTemp applies perm to, writes and fsyncs an already-created temporary file,
// always closing it exactly once. It is replaceAtomically's only path back to the caller
// before the rename, so every failure it returns leaves the rename unattempted.
func writeAndSyncTemp(tmp *os.File, data []byte, perm filePermission) (err error) {
	defer func() {
		if cerr := tmp.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("util: close temp file %q: %w", tmp.Name(), cerr)
		}
	}()
	if chErr := perm.apply(tmp); chErr != nil {
		return chErr
	}
	if _, werr := tmp.Write(data); werr != nil {
		return fmt.Errorf("util: write temp file %q: %w", tmp.Name(), werr)
	}
	if serr := tmp.Sync(); serr != nil {
		return fmt.Errorf("util: sync temp file %q: %w", tmp.Name(), serr)
	}
	return nil
}

// MkdirSecure creates path and missing parents, respecting the process umask. perm
// is a permission ceiling on the leaf: a pre-existing or newly created directory is
// only tightened, never widened. Existing ancestors retain their permissions.
//
// path is used as given: every existing ancestor, symbolic links included, is followed,
// because without a root there is nothing for a link to escape. A caller creating an
// untrusted relative path below a root uses MkdirConfined instead.
//
// gosec: addresses G301 (directory created with permissions above 0750). Call sites pass
// an explicit perm; perm == 0 selects SecureDirPerm.
func MkdirSecure(path string, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureDirPerm)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("util: create directory %q: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("util: stat directory %q: %w", path, err)
	}
	return tightenMode(path, info, perm, func(mode os.FileMode) error { return os.Chmod(path, mode) })
}

// MkdirConfined is MkdirSecure for rel below root, confined through the whole operation
// (BUG-826). rel first passes ConfinePath's check; the directories are then created, and
// the leaf tightened, through a pinned handle on root (os.Root), which follows a link only
// while it stays inside root. A component swapped for an escaping link after the check is
// refused instead of followed. perm is the same ceiling MkdirSecure applies to the leaf.
//
// root must exist. Its own path is resolved when it is opened (it is the caller's chosen
// boundary, and macOS ships /var and /tmp as links), and a rel naming root itself creates
// nothing and changes no mode. An in-root link with an absolute target is refused, since
// os.Root follows only relative links. An escape is ErrPathEscapesRoot whether the check or
// the pinned handle refuses it.
func MkdirConfined(root, rel string, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureDirPerm)
	if err != nil {
		return err
	}
	absRoot, inside, err := confineBelow(root, rel)
	if err != nil {
		return err
	}
	return mkdirConfined(absRoot, inside, perm)
}

// InConfinedDirectory runs fn with a pinned handle (os.Root) on the existing directory rel
// below root, and closes the handle afterwards. It is the confinement MkdirConfined and
// WriteFileConfined apply, lent to a caller that publishes through its own writer: rel
// first passes ConfinePath's check, then every component is opened through a pinned handle
// on root, so a component swapped for an escaping link after the check is refused instead
// of followed (BUG-826). An escape is ErrPathEscapesRoot whether the check or the pinned
// handle refuses it; any error fn returns is passed through.
func InConfinedDirectory(root, rel string, fn func(dir *os.Root) error) error {
	if fn == nil {
		return errors.New("util: a confined directory callback is required")
	}
	absRoot, inside, err := confineBelow(root, rel)
	if err != nil {
		return err
	}
	return inConfinedDirectory(absRoot, inside, fn)
}

// inConfinedDirectory is InConfinedDirectory after the check: inside is opened through the
// pinned handle on absRoot, and an escape the handle refuses is classified by
// classifyEscape.
func inConfinedDirectory(absRoot, inside string, fn func(dir *os.Root) error) error {
	return classifyEscape(absRoot, inside, inRoot(absRoot, inside, fn))
}

// mkdirConfined is MkdirConfined after the check: every component of inside resolves
// through the pinned handle on absRoot, and an escape the handle refuses is classified by
// classifyEscape.
func mkdirConfined(absRoot, inside string, perm os.FileMode) error {
	return classifyEscape(absRoot, inside, inRoot(absRoot, ".", func(base *os.Root) error {
		if inside == "." {
			return nil
		}
		if err := base.MkdirAll(inside, perm); err != nil {
			return fmt.Errorf("util: create directory %q below %q: %w", inside, absRoot, err)
		}
		info, err := base.Stat(inside)
		if err != nil {
			return fmt.Errorf("util: stat directory %q below %q: %w", inside, absRoot, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("util: %q below %q is not a directory", inside, absRoot)
		}
		return tightenMode(filepath.Join(absRoot, inside), info, perm, func(mode os.FileMode) error {
			return base.Chmod(inside, mode)
		})
	}))
}

// classifyEscape labels opErr, the failure of a pinned os.Root operation on inside, with
// ErrPathEscapesRoot when ConfinePath's check, run again now, finds inside escaping absRoot.
// os.Root reports a component that leaves the root with an unexported error, so the check
// that classifies an escape before the operation classifies it after the operation too: a
// link swapped in between the two comes back errors.Is(ErrPathEscapesRoot), exactly like
// the same link caught by the check. Any other failure, and a nil opErr, pass through.
func classifyEscape(absRoot, inside string, opErr error) error {
	if opErr == nil {
		return nil
	}
	if _, _, err := confine(absRoot, inside); errors.Is(err, ErrPathEscapesRoot) {
		return fmt.Errorf("%w: %w", err, opErr)
	}
	return opErr
}

// effectivePerm substitutes def for a zero perm and refuses what checkPerm refuses.
func effectivePerm(perm, def os.FileMode) (os.FileMode, error) {
	if perm == 0 {
		perm = def
	}
	if err := checkPerm(perm); err != nil {
		return 0, err
	}
	return perm, nil
}

// checkPerm rejects world-writable and non-permission mode bits.
func checkPerm(perm os.FileMode) error {
	if perm&^os.ModePerm != 0 {
		return fmt.Errorf("%w: %v carries non-permission mode bits", ErrInsecurePerm, perm)
	}
	if perm&0o002 != 0 {
		return fmt.Errorf("%w: %#o is world-writable", ErrInsecurePerm, perm)
	}
	return nil
}

// ValidateExecArg rejects a value that must not be handed to an external process as an
// argument: an empty string, a value that would be parsed as an option because it starts
// with '-', a value containing one of the shell metacharacters ;&|$`<>(){}"'\ , a value
// containing a control character, and a value longer than MaxExecArgLen.
//
// gosec: addresses G204 (subprocess launched with a variable). Validate every
// user-supplied or config-supplied value before appending it to an exec argument list;
// path-shaped values should additionally go through ConfinePath.
func ValidateExecArg(s string) error {
	return validateExecArg(s, execArgMetaChars)
}

// ValidateExecPathArg validates a filesystem path passed as an exec argument.
//
// ValidateExecArg treats a backslash as a shell metacharacter, which is right for the
// identifiers it guards -- package names, versions, URLs, repository owners -- and wrong
// for a path on Windows, where the backslash is the separator. Every absolute path there
// was refused, so internal/operationalsync could not hand git a checkout path at all.
//
// This exempts exactly one character: the host path separator. On POSIX that is '/',
// which is not in the metacharacter set, so the exemption removes nothing and this is
// identical to ValidateExecArg. On Windows only the backslash is exempted; every other
// metacharacter, a leading '-', control bytes and the length bound are still refused.
// Identifier callers must keep using ValidateExecArg: loosening it globally would have
// admitted backslashes into package names and URLs to fix a problem that only paths have.
func ValidateExecPathArg(s string) error {
	return validateExecArg(s, strings.ReplaceAll(execArgMetaChars, string(filepath.Separator), ""))
}

func validateExecArg(s, metaChars string) error {
	if s == "" {
		return ErrEmptyExecArg
	}
	if len(s) > MaxExecArgLen {
		return fmt.Errorf("%w: %d > %d", ErrExecArgTooLong, len(s), MaxExecArgLen)
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("%w: %q", ErrExecArgOption, s)
	}
	if idx := strings.IndexAny(s, metaChars); idx >= 0 {
		return fmt.Errorf("%w: %q at offset %d", ErrExecArgMeta, s[idx:idx+1], idx)
	}
	for i := 0; i < len(s) && i < MaxExecArgLen; i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return fmt.Errorf("%w: control byte %#02x at offset %d", ErrExecArgMeta, s[i], i)
		}
	}
	return nil
}
