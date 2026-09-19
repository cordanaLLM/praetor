package util

import (
	"errors"
	"fmt"
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
	// MaxExecArgLen bounds the length of a single externally supplied exec argument.
	MaxExecArgLen = 4096
	// maxPathAncestorWalk bounds the ancestor walk in ConfinePath (HISS-02: every loop
	// carries a scalar upper bound).
	maxPathAncestorWalk = 256
	// execArgMetaChars lists the shell metacharacters rejected by ValidateExecArg.
	execArgMetaChars = ";&|$`<>(){}\"'\\"
)

var (
	// ErrEmptyRoot is returned when ConfinePath is given an empty confinement root.
	ErrEmptyRoot = errors.New("util: confinement root must not be empty")
	// ErrAbsoluteRelPath is returned when ConfinePath is given an absolute member path.
	ErrAbsoluteRelPath = errors.New("util: member path must be relative to the root")
	// ErrPathEscapesRoot is returned when a member path resolves outside its root.
	ErrPathEscapesRoot = errors.New("util: path escapes the confinement root")
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
)

// ConfinePath joins rel onto root and returns the cleaned, absolute result, guaranteeing
// that the result cannot leave root either lexically (via "..") or through a symbolic
// link. Paths that do not exist yet are still confined: the check resolves the deepest
// existing ancestor and re-attaches the remaining segments.
//
// gosec: this is the canonical sanitizer for G304 (file inclusion via variable) and
// G305 (file traversal when extracting an archive). Pass user-, config- or
// archive-supplied relative paths through ConfinePath before handing them to os.Open,
// os.ReadFile, WriteFileSecure or MkdirSecure.
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

func ConfinePath(root, rel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", ErrEmptyRoot
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrAbsoluteRelPath, rel)
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("util: resolve confinement root %q: %w", root, err)
	}

	candidate := filepath.Clean(filepath.Join(absRoot, rel))
	if !withinRoot(absRoot, candidate) {
		return "", fmt.Errorf("%w: %q is not under %q", ErrPathEscapesRoot, candidate, absRoot)
	}

	resolvedRoot, err := resolveExistingAncestor(absRoot)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := resolveExistingAncestor(candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(resolvedRoot, resolvedCandidate) {
		return "", fmt.Errorf("%w: %q resolves to %q, outside %q",
			ErrPathEscapesRoot, candidate, resolvedCandidate, resolvedRoot)
	}
	return candidate, nil
}

// withinRoot reports whether p is root itself or a descendant of root. Both arguments
// must already be cleaned absolute paths.
func withinRoot(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
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
	if perm == 0 {
		perm = SecureFilePerm
	}
	if permErr := checkPerm(perm); permErr != nil {
		return permErr
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
	target := info.Mode().Perm() & perm
	if target == info.Mode().Perm() {
		return nil
	}
	if err := file.Chmod(target); err != nil {
		return fmt.Errorf("util: chmod %q to %#o: %w", file.Name(), target, err)
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
// perm is the same permission ceiling WriteFileSecure enforces: a zero perm selects
// SecureFilePerm, and world-writable or non-permission bits are refused. Unlike
// WriteFileSecure, perm is not intersected with any pre-existing file at path: the
// rename replaces that file outright, so its historical permission bits do not carry
// forward, matching every other atomic-rename writer.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if perm == 0 {
		perm = SecureFilePerm
	}
	if permErr := checkPerm(perm); permErr != nil {
		return permErr
	}

	dir := filepath.Dir(path)
	// #nosec G304 -- dir is the caller-confined directory of path; the pattern below is a
	// fixed prefix plus a directory-local base name, not attacker-controlled.
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("util: create temp file for %q: %w", path, err)
	}
	tmpPath := tmp.Name()

	if writeErr := writeAndSyncTemp(tmp, data, perm); writeErr != nil {
		os.Remove(tmpPath) //nolint:errcheck // best-effort; a leftover temp file self-heals on the next write attempt and must not mask writeErr
		return writeErr
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		os.Remove(tmpPath) //nolint:errcheck // best-effort; a leftover temp file self-heals on the next write attempt and must not mask renameErr
		return fmt.Errorf("util: rename %q to %q: %w", tmpPath, path, renameErr)
	}
	return nil
}

// writeAndSyncTemp chmods, writes and fsyncs an already-created temporary file, always
// closing it exactly once. It is WriteFileAtomic's only path back to the caller before
// the rename, so every failure it returns leaves the rename unattempted.
func writeAndSyncTemp(tmp *os.File, data []byte, perm os.FileMode) (err error) {
	defer func() {
		if cerr := tmp.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("util: close temp file %q: %w", tmp.Name(), cerr)
		}
	}()
	if chErr := tmp.Chmod(perm); chErr != nil {
		return fmt.Errorf("util: chmod temp file %q to %#o: %w", tmp.Name(), perm, chErr)
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
// gosec: addresses G301 (directory created with permissions above 0750). Call sites pass
// an explicit perm; perm == 0 selects SecureDirPerm.
func MkdirSecure(path string, perm os.FileMode) error {
	if perm == 0 {
		perm = SecureDirPerm
	}
	if err := checkPerm(perm); err != nil {
		return err
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("util: create directory %q: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("util: stat directory %q: %w", path, err)
	}
	target := info.Mode().Perm() & perm
	if target != info.Mode().Perm() {
		if err := os.Chmod(path, target); err != nil {
			return fmt.Errorf("util: chmod directory %q to %#o: %w", path, target, err)
		}
	}
	return nil
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
