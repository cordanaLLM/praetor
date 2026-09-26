package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	// DefaultCommandTimeout bounds any subprocess started through RunCommand whose
	// context does not already carry a deadline (HISS-02).
	DefaultCommandTimeout = 30 * time.Second
	// DefaultAuthTokenTimeout bounds the gh CLI token lookup, which can block on a
	// locked OS keyring.
	DefaultAuthTokenTimeout = 15 * time.Second
	// CommandWaitDelay bounds how long RunCommand waits for output pipes still held by
	// descendants after the direct child exited or the context expired. On Unix it is also
	// the grace a command asked to stop gets before it is killed: SIGTERM when its context
	// ends, or the signal that is ending this process (TerminateCommandsOnSignal).
	CommandWaitDelay = 5 * time.Second
)

// ErrRepoIdentityUnresolved is returned by ResolveRepoIdentity when neither the git
// origin remote nor the directory layout identifies an owner and repository.
var ErrRepoIdentityUnresolved = errors.New("util: unable to resolve repository owner and name")

// ErrSymlinkDestination is returned by WriteFileNoFollow when the destination exists and
// is a symbolic link, or exists and is not a regular file.
var ErrSymlinkDestination = errors.New("util: refusing to write through a non-regular destination")

// MaxErrorBodyBytes bounds how much of an HTTP error response body is read into memory, and
// how much command diagnostic text an error may embed.
const MaxErrorBodyBytes = 64 * 1024

// MaxErrorBodyPreview bounds how much of a forge response body BodyPreview embeds in an
// error string that ends up on a terminal or inside a receipt.
const MaxErrorBodyPreview = 256

// ReadErrorBody reads the excerpt of an HTTP error response that may be embedded in an
// error message. The read is bounded by MaxErrorBodyBytes, so neither a hostile nor a
// misconfigured endpoint can stream an unbounded body into memory, and the excerpt goes
// through BodyPreview, so it is bounded and free of control characters and escape
// sequences. A read failure is reported rather than silently yielding a truncated body
// (HISS-07).
func ReadErrorBody(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, MaxErrorBodyBytes))
	excerpt := BodyPreview(data)
	if err != nil {
		return fmt.Sprintf("%s [reading the response body failed: %s]", excerpt, BodyPreview([]byte(err.Error())))
	}
	return excerpt
}

// BodyPreview renders a bounded, control-character-free excerpt of a response body so that
// a hostile or misconfigured endpoint cannot flood a terminal or inject escape sequences.
// Line breaks and tabs become spaces, every other control character or invalid UTF-8 byte
// becomes '.', and a body longer than MaxErrorBodyPreview bytes is cut and marked.
func BodyPreview(body []byte) string {
	truncated := false
	if len(body) > MaxErrorBodyPreview {
		body = body[:MaxErrorBodyPreview]
		truncated = true
	}
	var sb strings.Builder
	for _, r := range string(body) {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			sb.WriteByte(' ')
		case unicode.IsControl(r) || r == unicode.ReplacementChar:
			sb.WriteByte('.')
		default:
			sb.WriteRune(r)
		}
	}
	out := strings.TrimSpace(sb.String())
	if truncated {
		out += "... (truncated)"
	}
	return out
}

// TruncateExcerpt shortens s to at most limit bytes, marking that it was cut.
func TruncateExcerpt(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "... [truncated]"
}

// WriteFileNoFollow writes data to path, refusing to write through a symbolic link.
//
// os.WriteFile follows symlinks and truncates their target, so a repository that ships a
// tracked ledger path (.workingdir/BACKLOG.md, .workingdir/milestones.json, docs/wiki/*)
// as a link to a file outside the repository can redirect praetor's own writes onto that
// file. The destination is inspected with an lstat that does not follow the final path
// component, and anything that exists but is not a regular file is rejected before the
// write.
//
// The write is atomic and pinned (BUG-826, BUG-827): path's directory is opened once as an
// os.Root, and the inspection, a temp-file write with fsync, and the rename onto path all
// resolve against that handle (WriteFileAtomic's implementation). A crash or a failed write
// leaves the previous ledger intact instead of truncated; an ancestor swapped for a
// symbolic link after the directory is opened cannot move the write; and a link planted at
// path after the inspection is replaced by the rename, never written through.
//
// path's directory is opened as given: its ancestors, symbolic links included, are
// followed, because without a root there is nothing for a link to escape. A caller writing
// an untrusted relative path below a root (a tracked ledger path inside a repository) uses
// WriteFileConfined, which keeps every ancestor confined to the root up to the write.
//
// perm keeps WriteFileSecure's ceiling: a zero perm selects SecureFilePerm, world-writable
// or non-permission bits are refused, a new file is created with perm under the process
// umask, and a replaced file keeps only the bits it already had that perm also grants.
// An existing file without its owner-write bit is refused with an error wrapping
// os.ErrPermission, as the in-place writer's open refused it, even though the rename itself
// would only need a writable directory. As with every rename-based writer, the directory
// must be writable and the replacement is a new inode: hard links to the old file keep the
// old contents, and its owner and extended attributes do not carry forward.
func WriteFileNoFollow(path string, data []byte, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureFilePerm)
	if err != nil {
		return err
	}
	return inParentDirectory(path, func(dir *os.Root, name string) error {
		return replaceNoFollow(dir, name, path, data, perm)
	})
}

// WriteFileConfined is WriteFileNoFollow for rel below root, confined through the whole
// write (BUG-826). rel first passes ConfinePath's check; its directory is then opened
// through a pinned handle on root (os.Root), which follows a link only while it stays
// inside root, and WriteFileNoFollow's inspection, atomic replace and permission ceiling
// run against that directory. An ancestor swapped for an escaping link after the check is
// refused instead of followed, which a ConfinePath result handed to WriteFileNoFollow
// cannot guarantee.
//
// rel's directory must exist; MkdirConfined creates it. root's own path is resolved when
// it is opened (it is the caller's chosen boundary, and macOS ships /var and /tmp as
// links). A rel naming root itself is refused with ErrRootItself, and an in-root link with
// an absolute target is refused too, since os.Root follows only relative links. An escape
// is ErrPathEscapesRoot whether the check or the pinned handle refuses it.
func WriteFileConfined(root, rel string, data []byte, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureFilePerm)
	if err != nil {
		return err
	}
	absRoot, inside, err := confineBelow(root, rel)
	if err != nil {
		return err
	}
	return writeConfined(absRoot, inside, data, perm)
}

// writeConfined is WriteFileConfined after the check: inside's directory resolves through
// the pinned handle on absRoot, and an escape the handle refuses is classified by
// classifyEscape.
func writeConfined(absRoot, inside string, data []byte, perm os.FileMode) error {
	if inside == "." {
		return fmt.Errorf("%w: %q", ErrRootItself, absRoot)
	}
	return classifyEscape(absRoot, inside, inRoot(absRoot, filepath.Dir(inside), func(dir *os.Root) error {
		return replaceNoFollow(dir, filepath.Base(inside), filepath.Join(absRoot, inside), data, perm)
	}))
}

// replaceNoFollow is the no-follow write shared by WriteFileNoFollow and WriteFileConfined:
// it refuses a destination that is a link or not a regular file and replaces name in dir
// atomically under the permission ceiling. path only labels errors.
func replaceNoFollow(dir *os.Root, name, path string, data []byte, perm os.FileMode) error {
	mode, err := noFollowPermission(dir, name, path, perm)
	if err != nil {
		return err
	}
	return replaceAtomically(dir, name, data, mode)
}

// noFollowPermission refuses a destination that exists but is a symbolic link, is not a
// regular file, or lacks its owner-write bit, and returns the mode its replacement takes:
// perm as a ceiling on the umask-filtered creation mode for a new file, or the existing
// bits intersected with perm for a file being replaced.
//
// The owner-write check keeps the write protection the in-place writer honored: a rename
// needs write permission on the directory only, so without it a file its owner made
// read-only would be replaced silently. Go reports a Windows read-only file without the
// bit as well, so the refusal is the same on every platform.
func noFollowPermission(dir *os.Root, name, path string, perm os.FileMode) (filePermission, error) {
	info, err := dir.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return filePermission{mode: perm}, nil
	case err != nil:
		return filePermission{}, fmt.Errorf("util: inspect write destination %q: %w", path, err)
	case info.Mode()&os.ModeSymlink != 0:
		return filePermission{}, fmt.Errorf("%w: %q is a symbolic link", ErrSymlinkDestination, path)
	case !info.Mode().IsRegular():
		return filePermission{}, fmt.Errorf("%w: %q is not a regular file", ErrSymlinkDestination, path)
	case info.Mode().Perm()&ownerWriteBit == 0:
		return filePermission{}, fmt.Errorf("util: %q is write-protected (mode %#o): %w",
			path, info.Mode().Perm(), os.ErrPermission)
	}
	return filePermission{mode: perm & info.Mode().Perm(), exact: true}, nil
}

// ReadFileNoFollow reads path, refusing to read through a symbolic link.
//
// It is the read-side counterpart of WriteFileNoFollow: a ledger or cache file that a
// hostile repository ships as a link to a file outside the tree must not be parsed as
// praetor's own state.
func ReadFileNoFollow(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("util: inspect %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %q is a symbolic link", ErrSymlinkDestination, path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrSymlinkDestination, path)
	}
	// #nosec G304 -- the destination has just been checked with os.Lstat to be a regular
	// file that is not a symbolic link; callers pass repository-local ledger paths.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("util: read %q: %w", path, err)
	}
	return data, nil
}

// PathExists returns true if path exists on the filesystem.
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileExists returns true if path exists and is not a directory.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DirExists returns true if path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// CleanGitURL normalizes git remote URLs (SSH, HTTPS, git://).
func CleanGitURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimSuffix(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	return strings.TrimSuffix(trimmed, "/")
}

// ErrGitRemoteNotNetwork is returned by ParseGitRemote for a remote that does not name a
// network host: a local path, a file:// URL, a Windows drive path or an unsupported scheme.
var ErrGitRemoteNotNetwork = errors.New("util: git remote does not name a network host and repository")

// maxRemoteExcerptBytes bounds how much of a rejected remote an error repeats.
const maxRemoteExcerptBytes = 256

// GitRemote is a network git remote split into the host it talks to and the repository
// path on that host.
type GitRemote struct {
	// Host is the lower-cased host name, without user info, port or trailing dot.
	Host string
	// Path is the repository path on Host without surrounding slashes or ".git", such as
	// "acme/widgets", or "group/subgroup/widgets" on a forge with nested namespaces.
	Path string
	// Owner and Repo are the last two segments of Path, as ExtractOwnerAndRepo reports them.
	Owner string
	Repo  string
}

// gitRemoteSchemes are the URL schemes git uses to reach a network host.
var gitRemoteSchemes = map[string]bool{"https": true, "http": true, "ssh": true, "git": true, "git+ssh": true}

// ParseGitRemote parses a network git remote, as ParseGitRemoteURL accepts it, into its
// lower-cased host and its repository path without ".git". A remote that is not a
// network remote, or whose path has fewer than two segments, is ErrGitRemoteNotNetwork,
// so a caller that compares the host can never mistake a directory for a forge.
func ParseGitRemote(raw string) (GitRemote, error) {
	parsed, err := ParseGitRemoteURL(raw)
	if err != nil {
		return GitRemote{}, err
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	path := strings.Trim(CleanGitURL(parsed.Path), "/")
	owner, repo := extractPathStyle(path)
	if host == "" || owner == "" || repo == "" {
		return GitRemote{}, fmt.Errorf("%w: %q lacks a host or an <owner>/<repo> path", ErrGitRemoteNotNetwork, remoteExcerpt(raw))
	}
	return GitRemote{Host: host, Path: path, Owner: owner, Repo: repo}, nil
}

// ParseGitRemoteURL parses a network git remote into a URL: a URL with an https, http,
// ssh, git or git+ssh scheme, or git's scp-like [user@]host:path form, which becomes
// ssh://host/path. User info, query and fragment are removed; the host's case, the port
// and the path, ".git" included, stay as written. Anything else, including a local path,
// a file:// URL, a Windows drive path and a remote-helper address such as ext::..., is
// ErrGitRemoteNotNetwork. The error repeats the remote only with its user info redacted.
func ParseGitRemoteURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, reason := parseNetworkRemote(trimmed)
	if reason != "" {
		return nil, fmt.Errorf("%w: %q: %s", ErrGitRemoteNotNetwork, remoteExcerpt(trimmed), reason)
	}
	return parsed, nil
}

// parseNetworkRemote does ParseGitRemoteURL's work and reports a rejection as a fixed
// reason, never as an underlying error: net/url quotes the whole raw URL in its errors,
// user info included.
func parseNetworkRemote(trimmed string) (*url.URL, string) {
	if trimmed == "" || strings.ContainsFunc(trimmed, unicode.IsControl) || strings.ContainsAny(trimmed, " \\") {
		return nil, "empty, or contains a control character, space or backslash"
	}
	rawURL := trimmed
	if !strings.Contains(trimmed, "://") {
		var reason string
		if rawURL, reason = scpRemoteURL(trimmed); reason != "" {
			return nil, reason
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "not a well-formed URL"
	}
	if reason := networkRemoteReason(parsed); reason != "" {
		return nil, reason
	}
	parsed.User = nil
	parsed.RawQuery, parsed.Fragment, parsed.RawFragment, parsed.ForceQuery = "", "", "", false
	return parsed, ""
}

// networkRemoteReason explains why a parsed URL is not a network git remote, or returns "".
func networkRemoteReason(parsed *url.URL) string {
	if !gitRemoteSchemes[strings.ToLower(parsed.Scheme)] || parsed.Opaque != "" {
		return "scheme is not a network git transport"
	}
	// Only a bracketed IPv6 literal may leave a colon in the host name.
	host := parsed.Hostname()
	if host == "" || (strings.Contains(host, ":") && !strings.HasPrefix(parsed.Host, "[")) {
		return "no valid host"
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return "no repository path"
	}
	return ""
}

// scpRemoteURL rewrites git's scp-like [user@]host:path as ssh://host/path, or explains
// why the string is not one.
func scpRemoteURL(trimmed string) (string, string) {
	// git reads host:path as scp-like only when no slash precedes the first colon; any
	// other string without "://" is a local path.
	hostPart, path, found := strings.Cut(trimmed, ":")
	if !found || strings.Contains(hostPart, "/") {
		return "", "local path"
	}
	if strings.HasPrefix(path, ":") {
		return "", "remote-helper address <transport>::<address>"
	}
	if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if len(hostPart) < 2 || strings.ContainsAny(hostPart, "[]?#") {
		// A one-letter host is a Windows drive such as C:, which git treats as local.
		return "", "drive letter or invalid scp host"
	}
	if strings.Contains(path, "@") {
		// user:password@host:path reads as host "user"; refuse it rather than carry the
		// password on as part of a path.
		return "", "scp path contains @"
	}
	return "ssh://" + hostPart + "/" + strings.TrimPrefix(path, "/"), ""
}

// remoteExcerpt renders a remote for an error message with everything up to its last "@"
// redacted, since user info may carry a token, and bounded to maxRemoteExcerptBytes. The
// last "@" is used because a password may itself contain "/" or ":".
func remoteExcerpt(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if at := strings.LastIndexByte(trimmed, '@'); at >= 0 {
		prefix := ""
		if scheme, _, found := strings.Cut(trimmed[:at], "://"); found && !strings.ContainsAny(scheme, "@:/") {
			prefix = scheme + "://"
		}
		trimmed = prefix + "<redacted>@" + trimmed[at+1:]
	}
	return TruncateExcerpt(trimmed, maxRemoteExcerptBytes)
}

// ExtractOwnerAndRepo returns owner and repository name from a git URL. A network remote
// goes through ParseGitRemote; any other string, such as a local path, falls back to its
// last two slash-separated segments.
func ExtractOwnerAndRepo(raw string) (owner, repo string) {
	if remote, err := ParseGitRemote(raw); err == nil {
		return remote.Owner, remote.Repo
	}
	trimmed := CleanGitURL(raw)
	if trimmed == "" {
		return "", ""
	}
	if o, r, ok := extractSSHStyle(trimmed); ok {
		return o, r
	}
	return extractPathStyle(trimmed)
}

// extractSSHStyle handles scp-like remotes such as git@github.com:owner/repo.
func extractSSHStyle(trimmed string) (owner, repo string, ok bool) {
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return "", "", false
	}
	idx := strings.LastIndex(trimmed, ":")
	if idx == -1 {
		return "", "", false
	}
	parts := strings.Split(trimmed[idx+1:], "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1], true
	}
	if len(parts) == 1 && parts[0] != "" {
		return "", parts[0], true
	}
	return "", "", false
}

// extractPathStyle handles URL-shaped remotes by taking the last two path segments.
func extractPathStyle(trimmed string) (owner, repo string) {
	parts := strings.Split(trimmed, "/")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) >= 2 {
		return filtered[len(filtered)-2], filtered[len(filtered)-1]
	}
	if len(filtered) == 1 {
		return "", filtered[0]
	}
	return "", ""
}

// maxCommandDiagnosticBytes bounds how much of a failed command's standard error RunCommand
// embeds in its error, on the same terms MaxErrorBodyBytes bounds an HTTP error body.
const maxCommandDiagnosticBytes = MaxErrorBodyBytes

// RunCommand executes a command and returns its trimmed standard output.
//
// Standard error never mixes into the returned text, which callers parse as paths, JSON or
// tokens: a warning printed by a successful run is not data (BUG-847). When the command
// fails, the error carries up to 64 KiB of its standard error, so the reason still reaches
// whoever reports the failure, and the returned text is the standard output produced
// before the failure. errors.Is and errors.As still see the underlying *exec.ExitError and
// context error.
//
// RunCommand shares RunCommandBytes' execution boundary (HISS-19). HISS-02: a nil context,
// or one without a deadline, is given DefaultCommandTimeout. Each stream is capped at
// MaxCommandOutputBytes and an overflow cancels the command. On Unix the child runs in its
// own process group. Cancellation sends that group SIGTERM, so git can remove its locks,
// and the child is killed if it is still running CommandWaitDelay later; the group is
// killed on return, so a grandchild cannot outlive the call (BUG-889). The group no longer
// receives a terminal's Ctrl-C, so a program's main calls TerminateCommandsOnSignal to
// forward that signal to it.
// The child's environment follows commandEnvironment: without WithCommandEnvironment it
// inherits the ambient one minus the variables that bind git to a repository (BUG-886).
func RunCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	// ensureDeadline also turns a nil context, which runBoundedCommand refuses, into one.
	ctx, cancel := ensureDeadline(ctx, DefaultCommandTimeout)
	defer cancel()
	result, err := runBoundedCommand(ctx, dir, name, MaxCommandOutputBytes, commandStreams{}, args)
	stdout := strings.TrimSpace(string(result.Stdout))
	if err != nil {
		return stdout, withCommandDiagnostic(err, result.Stderr)
	}
	return stdout, nil
}

// withCommandDiagnostic appends a failed command's trimmed, bounded standard error to err.
func withCommandDiagnostic(err error, stderr []byte) error {
	diagnostic := strings.TrimSpace(string(stderr))
	if diagnostic == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, TruncateExcerpt(diagnostic, maxCommandDiagnosticBytes))
}

// ensureDeadline returns a context guaranteed to carry a deadline. A nil context or one
// without a deadline (context.Background/TODO) is given fallback; an existing deadline is
// preserved. The returned cancel func is always non-nil and must be called.
func ensureDeadline(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, fallback)
}

// RunGit executes a git command through RunCommand and returns its trimmed standard output.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	return RunCommand(ctx, dir, "git", args...)
}

// RunGitBytes executes a git command through RunCommandBytes: the bounded-output form of
// RunGit, for a caller that needs standard output and standard error apart and byte-exact
// (NUL-separated listings, a probe whose empty streams carry meaning) or a cap below
// MaxCommandOutputBytes. It runs under the caller's environment exactly as RunGit does;
// RunGitProbe builds its isolated, read-only inspection on top of it.
func RunGitBytes(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error) {
	return RunCommandBytes(ctx, dir, "git", maxBytes, args...)
}

// ResolveRepoIdentity extracts the owner and repository name from the configured origin
// remote, falling back to the <owner>/<repo> shape of the absolute directory path.
//
// It never invents an owner: when neither the remote nor the directory layout yields
// one, it returns ErrRepoIdentityUnresolved so that callers writing to a forge refuse to
// publish into a guessed repository.
func ResolveRepoIdentity(ctx context.Context, repoPath string) (owner, repo string, err error) {
	out, gitErr := RunGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if gitErr == nil && out != "" {
		o, r := ExtractOwnerAndRepo(out)
		if o != "" && r != "" {
			return o, r, nil
		}
	}

	absPath, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", "", fmt.Errorf("%w: resolve %q: %w", ErrRepoIdentityUnresolved, repoPath, absErr)
	}
	normPath := filepath.Clean(absPath)
	repo = filepath.Base(normPath)
	parent := filepath.Base(filepath.Dir(normPath))
	if !isIdentitySegment(repo) || !isIdentitySegment(parent) || parent == "dev" {
		return "", "", fmt.Errorf("%w: no origin remote in %q and the path lacks an <owner>/<repo> shape",
			ErrRepoIdentityUnresolved, repoPath)
	}
	return parent, repo, nil
}

// isIdentitySegment reports whether a path segment can serve as an owner or repo name.
func isIdentitySegment(segment string) bool {
	return segment != "" && segment != "." && segment != ".." && segment != string(filepath.Separator)
}

// ResolveAuthToken resolves an authentication token from an explicit string, the
// GITHUB_TOKEN / GH_TOKEN environment variables, or the gh CLI session, in that order.
//
// HISS-02: the gh invocation is bounded by DefaultAuthTokenTimeout. Prefer
// ResolveAuthTokenContext when the caller already owns a context.
func ResolveAuthToken(explicitToken string) string {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAuthTokenTimeout)
	defer cancel()
	return ResolveAuthTokenContext(ctx, explicitToken)
}

// ResolveAuthTokenContext resolves an authentication token under the caller's context.
// The gh CLI fallback inherits the context deadline, or DefaultCommandTimeout when the
// context carries none.
func ResolveAuthTokenContext(ctx context.Context, explicitToken string) string {
	if explicitToken != "" {
		return explicitToken
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	if out, err := RunCommand(ctx, "", "gh", "auth", "token"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// commandEnvironmentKey scopes subprocess environment to one operation tree.
type commandEnvironmentKey struct{}

// WithCommandEnvironment makes RunCommand, RunGit, RunCommandBytes and RunCommandStream use
// exactly environment for this context's child processes, in place of the ambient
// environment minus the git repository variables. It is also how a caller opts back into an
// inherited GIT_DIR. It copies the input and never changes process-wide state.
func WithCommandEnvironment(ctx context.Context, environment []string) (context.Context, error) {
	if ctx == nil {
		return nil, errors.New("command environment requires a context")
	}
	const maxEnvironmentEntries = 256
	if len(environment) > maxEnvironmentEntries {
		return nil, errors.New("command environment exceeds 256 entries")
	}
	copied := make([]string, len(environment))
	copy(copied, environment)
	return context.WithValue(ctx, commandEnvironmentKey{}, copied), nil
}
