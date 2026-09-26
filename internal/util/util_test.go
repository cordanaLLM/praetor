package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFileAndDirExists(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "sample.txt")
	dirPath := filepath.Join(tmp, "subdir")

	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.Mkdir(dirPath, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	// Positive
	if !FileExists(filePath) {
		t.Errorf("expected FileExists(%s) == true", filePath)
	}
	if !DirExists(dirPath) {
		t.Errorf("expected DirExists(%s) == true", dirPath)
	}

	// Negative
	if FileExists(dirPath) {
		t.Errorf("expected FileExists(dirPath) == false")
	}
	if DirExists(filePath) {
		t.Errorf("expected DirExists(filePath) == false")
	}

	// Boundary (non-existent)
	nonExistent := filepath.Join(tmp, "does_not_exist")
	if FileExists(nonExistent) {
		t.Errorf("expected FileExists(nonExistent) == false")
	}
	if DirExists(nonExistent) {
		t.Errorf("expected DirExists(nonExistent) == false")
	}

	// PathExists
	if !PathExists(filePath) {
		t.Errorf("expected PathExists(filePath) == true")
	}
	if !PathExists(dirPath) {
		t.Errorf("expected PathExists(dirPath) == true")
	}
	if PathExists(nonExistent) {
		t.Errorf("expected PathExists(nonExistent) == false")
	}
}

func TestCleanGitURLAndExtract(t *testing.T) {
	tests := []struct {
		url       string
		wantClean string
		wantOwner string
		wantRepo  string
	}{
		{
			url:       "git@github.com:cordanaLLM/praetor.git",
			wantClean: "git@github.com:cordanaLLM/praetor",
			wantOwner: "cordanaLLM",
			wantRepo:  "praetor",
		},
		{
			url:       "https://github.com/golusoris/sveltesentio.git/",
			wantClean: "https://github.com/golusoris/sveltesentio",
			wantOwner: "golusoris",
			wantRepo:  "sveltesentio",
		},
		{
			url:       "vmafx",
			wantClean: "vmafx",
			wantOwner: "",
			wantRepo:  "vmafx",
		},
		{
			url:       "",
			wantClean: "",
			wantOwner: "",
			wantRepo:  "",
		},
	}

	for _, tt := range tests {
		gotClean := CleanGitURL(tt.url)
		if gotClean != tt.wantClean {
			t.Errorf("CleanGitURL(%q) = %q, want %q", tt.url, gotClean, tt.wantClean)
		}
		gotOwner, gotRepo := ExtractOwnerAndRepo(tt.url)
		if gotOwner != tt.wantOwner || gotRepo != tt.wantRepo {
			t.Errorf("ExtractOwnerAndRepo(%q) = (%q, %q), want (%q, %q)", tt.url, gotOwner, gotRepo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestParseGitRemote_Positive_NetworkForms(t *testing.T) {
	tests := []struct {
		raw  string
		want GitRemote
	}{
		{"https://github.com/acme/widgets.git", GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"git@github.com:acme/widgets.git", GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"ssh://git@github.com:22/acme/widgets", GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"git+ssh://GitHub.COM./acme/widgets", GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"https://x-access-token:secret@github.com/acme/widgets", GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"git://ghe.example.com/acme/widgets", GitRemote{Host: "ghe.example.com", Path: "acme/widgets", Owner: "acme", Repo: "widgets"}},
		{"https://gitlab.com/group/sub/widgets", GitRemote{Host: "gitlab.com", Path: "group/sub/widgets", Owner: "sub", Repo: "widgets"}},
	}
	for _, tt := range tests {
		got, err := ParseGitRemote(tt.raw)
		if err != nil {
			t.Errorf("ParseGitRemote(%q): %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseGitRemote(%q) = %+v, want %+v", tt.raw, got, tt.want)
		}
		// ExtractOwnerAndRepo delegates, so both views of a network remote agree.
		if owner, repo := ExtractOwnerAndRepo(tt.raw); owner != got.Owner || repo != got.Repo {
			t.Errorf("ExtractOwnerAndRepo(%q) = (%q, %q), ParseGitRemote says (%q, %q)", tt.raw, owner, repo, got.Owner, got.Repo)
		}
	}
}

func TestParseGitRemote_Negative_NotANetworkRemote(t *testing.T) {
	for _, raw := range []string{
		"/srv/git/acme/widgets",
		"../acme/widgets",
		"acme/widgets",
		"file:///srv/git/acme/widgets",
		"ext::ssh -o ProxyCommand=x github.com/acme/widgets",
		"C:/repos/acme/widgets",
		`C:\repos\acme\widgets`,
		"https://github.com",
		"https://github.com/widgets",
		"git@github.com:widgets",
		"https://github.com/acme\n/widgets",
		"https://github.com/acme/wid\x00gets",
		"[::1]:acme/widgets",
	} {
		got, err := ParseGitRemote(raw)
		if !errors.Is(err, ErrGitRemoteNotNetwork) {
			t.Errorf("ParseGitRemote(%q) = %+v, %v; want ErrGitRemoteNotNetwork", raw, got, err)
		}
	}
}

func TestParseGitRemote_Boundary(t *testing.T) {
	// Surrounding whitespace, a trailing slash and ".git" are all cleaned first.
	got, err := ParseGitRemote("  git@github.com:acme/widgets.git/\n")
	if err != nil || got.Path != "acme/widgets" || got.Host != "github.com" {
		t.Fatalf("ParseGitRemote with surrounding noise = %+v, %v", got, err)
	}
	// Empty input is rejected, not reported as an empty identity.
	if _, err := ParseGitRemote(""); !errors.Is(err, ErrGitRemoteNotNetwork) {
		t.Fatalf("empty remote: %v", err)
	}
	// A rejected URL never repeats its user info, and the excerpt stays bounded.
	_, err = ParseGitRemote("file://user:ghp_secret@host/" + strings.Repeat("a", 4*maxRemoteExcerptBytes))
	if err == nil || strings.Contains(err.Error(), "ghp_secret") {
		t.Fatalf("rejection leaked user info: %v", err)
	}
	if len(err.Error()) > 3*maxRemoteExcerptBytes {
		t.Fatalf("rejection excerpt is unbounded (%d bytes)", len(err.Error()))
	}
	// Two-letter scp hosts are hosts, one-letter ones are drive letters.
	if got, err := ParseGitRemote("gh:acme/widgets"); err != nil || got.Host != "gh" {
		t.Fatalf("two-letter scp host = %+v, %v", got, err)
	}
	// Local paths still resolve through ExtractOwnerAndRepo's fallback.
	if owner, repo := ExtractOwnerAndRepo("/srv/git/acme/widgets"); owner != "acme" || repo != "widgets" {
		t.Fatalf("local path fallback = (%q, %q)", owner, repo)
	}
}

// TestParseGitRemote_Negative_ParseErrorsRedactUserInfo covers remotes that reach the URL
// parser and fail there. net/url quotes the whole raw URL in its error, token included,
// so ParseGitRemote must not pass that error on.
func TestParseGitRemote_Negative_ParseErrorsRedactUserInfo(t *testing.T) {
	const secret = "ghp_SECRET"
	for _, raw := range []string{
		"https://x-access-token:" + secret + "@github.com:badport/acme/widgets",
		"https://x-access-token:" + secret + "@github.com/acme/wid%zzgets",
		"https://x-access-token:" + secret + "@[::1/acme/widgets",
		"ssh://git:" + secret + "@github.com:22:22/acme/widgets",
		// A slash inside the password moves the parser's host boundary; the excerpt
		// still drops everything up to the last @.
		"https://x-access-token:" + secret + "/x@github.com:badport/acme/widgets",
		// scp-like form with a password: git reads host "user", so this is no network
		// remote, and the password stays out of the error.
		"user:" + secret + "@github.com:acme/widgets",
	} {
		_, err := ParseGitRemote(raw)
		if !errors.Is(err, ErrGitRemoteNotNetwork) {
			t.Errorf("ParseGitRemote(%q) = %v; want ErrGitRemoteNotNetwork", raw, err)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("ParseGitRemote(%q) leaked user info: %v", raw, err)
		}
	}
}

func TestParseGitRemoteURL_Positive_RendersWithoutUserInfo(t *testing.T) {
	tests := []struct{ raw, want string }{
		// The path is kept as written, ".git" included, and the port survives.
		{"https://x-access-token:secret@example.invalid:8443/org/repo.git", "https://example.invalid:8443/org/repo.git"},
		{"git@example.invalid:org/repo.git", "ssh://example.invalid/org/repo.git"},
		{"git@example.invalid:/org/repo", "ssh://example.invalid/org/repo"},
		{"ssh://user:pw@example.invalid/org/repo?q=1#frag", "ssh://example.invalid/org/repo"},
		{"git://example.invalid/org/repo", "git://example.invalid/org/repo"},
		{"git+ssh://example.invalid/org/repo", "git+ssh://example.invalid/org/repo"},
		// A single path segment is still a network remote; ParseGitRemote is the one
		// that needs <owner>/<repo>.
		{"https://example.invalid/repo", "https://example.invalid/repo"},
	}
	for _, tt := range tests {
		got, err := ParseGitRemoteURL(tt.raw)
		if err != nil {
			t.Errorf("ParseGitRemoteURL(%q): %v", tt.raw, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("ParseGitRemoteURL(%q) = %q, want %q", tt.raw, got.String(), tt.want)
		}
	}
}

func TestParseGitRemoteURL_Negative_NotANetworkRemote(t *testing.T) {
	for _, raw := range []string{
		"/srv/git/org/repo",
		"file:///srv/git/org/repo",
		"C:/repos/org/repo",
		"ext::ssh-helper/org/repo",
		"host:org/repo@evil",
		"https://example.invalid",
		"https://example.invalid/",
		"mailto:someone@example.invalid",
		"ftp://example.invalid/org/repo",
	} {
		if got, err := ParseGitRemoteURL(raw); !errors.Is(err, ErrGitRemoteNotNetwork) {
			t.Errorf("ParseGitRemoteURL(%q) = %v, %v; want ErrGitRemoteNotNetwork", raw, got, err)
		}
	}
}

func TestParseGitRemoteURL_Boundary(t *testing.T) {
	// A two-letter scp host is a host, a one-letter one is a Windows drive.
	if got, err := ParseGitRemoteURL("gh:org/repo"); err != nil || got.Host != "gh" {
		t.Fatalf("two-letter scp host = %v, %v", got, err)
	}
	if _, err := ParseGitRemoteURL("c:org/repo"); !errors.Is(err, ErrGitRemoteNotNetwork) {
		t.Fatalf("one-letter scp host accepted: %v", err)
	}
	// Surrounding whitespace is trimmed; the host keeps its case for the caller to fold.
	got, err := ParseGitRemoteURL("  https://Example.Invalid/org/repo \n")
	if err != nil || got.String() != "https://Example.Invalid/org/repo" {
		t.Fatalf("trimmed remote = %v, %v", got, err)
	}
	// The error excerpt is bounded.
	_, err = ParseGitRemoteURL("ftp://example.invalid/" + strings.Repeat("a", 4*maxRemoteExcerptBytes))
	if err == nil || len(err.Error()) > 3*maxRemoteExcerptBytes {
		t.Fatalf("rejection excerpt is unbounded: %v", err)
	}
}

func TestRunCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmdName := "echo"
	args := []string{"hello-world"}
	expected := "hello-world"
	if os.PathSeparator == '\\' {
		cmdName = "cmd"
		args = []string{"/c", "echo", "hello-world"}
	}

	out, err := RunCommand(ctx, ".", cmdName, args...)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestRunGit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := RunGit(ctx, ".", "version")
	if err != nil {
		t.Fatalf("RunGit failed: %v", err)
	}
	if !strings.HasPrefix(out, "git version") {
		t.Errorf("expected git version string, got %q", out)
	}
}

// newHermeticGitRepo initialises an isolated git repository in a temp dir with the given
// origin URL. It never reads or writes the developer's global git configuration.
func newHermeticGitRepo(t *testing.T, originURL string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "nonexistent-gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "nonexistent-gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", originURL},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, string(out))
		}
	}
	return dir
}

func TestResolveRepoIdentity_Positive_ReadsOriginRemote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	repoDir := newHermeticGitRepo(t, "git@github.com:acme/widget.git")
	owner, repo, err := ResolveRepoIdentity(ctx, repoDir)
	if err != nil {
		t.Fatalf("ResolveRepoIdentity failed: %v", err)
	}
	if owner != "acme" || repo != "widget" {
		t.Errorf("got %s/%s, want acme/widget", owner, repo)
	}

	// The remote must win over the directory layout, which here is <tmp>/<random>.
	httpsRepo := newHermeticGitRepo(t, "https://github.com/golusoris/sveltesentio.git")
	owner, repo, err = ResolveRepoIdentity(ctx, httpsRepo)
	if err != nil {
		t.Fatalf("ResolveRepoIdentity (https remote) failed: %v", err)
	}
	if owner != "golusoris" || repo != "sveltesentio" {
		t.Errorf("got %s/%s, want golusoris/sveltesentio", owner, repo)
	}
}

func TestResolveRepoIdentity_Negative_NeverGuessesOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// A repository-shaped path with no remote falls back to the directory layout, and
	// the derived owner is the real parent directory, never a hard-coded default.
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "acme-org", "widget-lib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	owner, repo, err := ResolveRepoIdentity(ctx, nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme-org" || repo != "widget-lib" {
		t.Errorf("got %s/%s, want acme-org/widget-lib", owner, repo)
	}

	// A path whose parent is the conventional scratch directory name carries no owner
	// information, so the identity must be refused rather than fabricated.
	devNested := filepath.Join(tmp, "dev", "widget-lib")
	if err := os.MkdirAll(devNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if o, r, err := ResolveRepoIdentity(ctx, devNested); !errors.Is(err, ErrRepoIdentityUnresolved) {
		t.Errorf("expected ErrRepoIdentityUnresolved for %q, got (%q, %q, %v)", devNested, o, r, err)
	}
}

func TestResolveRepoIdentity_Boundary_RootAndRelativeDot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Boundary: the filesystem root has no owner segment.
	if o, r, err := ResolveRepoIdentity(ctx, string(filepath.Separator)); !errors.Is(err, ErrRepoIdentityUnresolved) {
		t.Errorf("expected ErrRepoIdentityUnresolved for the filesystem root, got (%q, %q, %v)", o, r, err)
	}

	// Boundary: a relative "." must be resolved to an absolute path before deriving the
	// repo name, so the repo name can never be literally ".".
	repoDir := newHermeticGitRepo(t, "https://example.invalid/x")
	sub := filepath.Join(repoDir, "acme", "gadget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if chErr := os.Chdir(prev); chErr != nil {
			t.Logf("restore working directory: %v", chErr)
		}
	})
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	owner, repo, err := ResolveRepoIdentity(ctx, ".")
	if err != nil {
		t.Fatalf("unexpected error for relative dot: %v", err)
	}
	if repo == "." || repo == "" {
		t.Errorf("repo must be a real directory name, got %q", repo)
	}
	if owner == "" {
		t.Errorf("owner must not be empty, got %q/%q", owner, repo)
	}
}

func TestRunCommand_Negative(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Missing binary.
	if _, err := RunCommand(ctx, ".", "praetor-no-such-binary-xyz"); err == nil {
		t.Error("expected error for a non-existent binary, got nil")
	}

	// Non-zero exit status.
	if _, err := RunCommand(ctx, t.TempDir(), "git", "rev-parse", "--verify", "refs/heads/praetor-no-such-branch"); err == nil {
		t.Error("expected error for a failing git invocation, got nil")
	}

	// Already cancelled context.
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := RunCommand(cancelled, ".", "git", "version"); err == nil {
		t.Error("expected error for a cancelled context, got nil")
	}
}

func TestRunCommand_Boundary_NilContextAndDeadline(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell semantics required")
	}

	// Boundary: a nil context is accepted and bounded by DefaultCommandTimeout.
	out, err := RunCommand(nil, ".", "echo", "nil-ctx") //nolint:staticcheck // exercising the nil-context contract
	if err != nil {
		t.Fatalf("RunCommand with nil context failed: %v", err)
	}
	if out != "nil-ctx" {
		t.Errorf("got %q, want %q", out, "nil-ctx")
	}

	// Boundary: an expired deadline must abort the subprocess rather than wait for it.
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := RunCommand(short, ".", "sleep", "30"); err == nil {
		t.Error("expected a deadline error running sleep 30, got nil")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("deadline was not honoured: elapsed %v", elapsed)
	}
}

// A warning printed by a successful run is not data: callers parse RunCommand's text as
// paths, JSON and tokens (BUG-847).
func TestRunCommand_Positive_ReturnsStandardOutputOnly(t *testing.T) {
	ctx, binary := bytesHelper(t, "warn")
	out, err := RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	if err != nil {
		t.Fatal(err)
	}
	if out != "value" {
		t.Fatalf("standard error leaked into the output: %q", out)
	}
}

// A failure keeps its exit status and carries its standard error in the error text, and the
// returned text is the standard output produced before the failure.
func TestRunCommand_Negative_FailureCarriesStandardError(t *testing.T) {
	ctx, binary := bytesHelper(t, "failure")
	out, err := RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 8 {
		t.Fatalf("exit status lost: %v", err)
	}
	if out != "partial" || !strings.Contains(err.Error(), "failure evidence") {
		t.Fatalf("failure evidence lost: %q, %v", out, err)
	}
}

func TestRunCommand_Boundary_DiagnosticAndOutputCaps(t *testing.T) {
	ctx, binary := bytesHelper(t, "loudfailure")
	_, err := RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	if err == nil {
		t.Fatal("failing command reported success")
	}
	if message := err.Error(); !strings.HasSuffix(message, "... [truncated]") ||
		len(message) > maxCommandDiagnosticBytes+256 {
		t.Fatalf("diagnostic not bounded: %d bytes", len(message))
	}
	ctx, binary = bytesHelper(t, "atcap")
	out, err := RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	if err != nil || len(out) != MaxCommandOutputBytes {
		t.Fatalf("output at the cap rejected: %d bytes, %v", len(out), err)
	}
	ctx, binary = bytesHelper(t, "overcap")
	out, err = RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	if err == nil || !strings.Contains(err.Error(), "exceeds") || len(out) > MaxCommandOutputBytes {
		t.Fatalf("output past the cap accepted: %d bytes, %v", len(out), err)
	}
}

// A descendant that holds the output pipe must neither keep the call waiting for
// CommandWaitDelay nor outlive the deadline (BUG-889).
func TestRunCommand_Boundary_KillsDescendantsOnDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is Unix-only (command_bytes_unix.go); Windows keeps " +
			"direct-child cancellation bounded by CommandWaitDelay")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunCommand(ctx, root, "sh", "-c", "(sleep 0.3; touch leaked) & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not reported: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= CommandWaitDelay {
		t.Fatalf("waited %v for a descendant holding the output pipe", elapsed)
	}
	time.Sleep(600 * time.Millisecond)
	if _, statErr := os.Stat(filepath.Join(root, "leaked")); !os.IsNotExist(statErr) {
		t.Fatalf("a descendant survived the deadline: %v", statErr)
	}
}

func TestEnsureDeadline_3D(t *testing.T) {
	// Positive: a context without a deadline receives the fallback.
	ctx, cancel := ensureDeadline(context.Background(), 42*time.Second)
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline on a background context")
	}
	if remaining := time.Until(dl); remaining <= 0 || remaining > 42*time.Second {
		t.Errorf("unexpected remaining budget %v", remaining)
	}

	// Negative/preservation: an existing deadline is never widened.
	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelParent()
	child, cancelChild := ensureDeadline(parent, time.Hour)
	defer cancelChild()
	childDL, ok := child.Deadline()
	if !ok {
		t.Fatal("expected the parent deadline to be preserved")
	}
	if time.Until(childDL) > time.Second {
		t.Errorf("fallback overrode a tighter parent deadline: %v", time.Until(childDL))
	}

	// Boundary: a nil context is promoted to a deadline-bearing background context.
	nilCtx, cancelNil := ensureDeadline(nil, time.Second) //nolint:staticcheck // exercising the nil-context contract
	defer cancelNil()
	if _, ok := nilCtx.Deadline(); !ok {
		t.Error("expected a deadline for a nil context")
	}
}

func TestResolveAuthToken_Precedence(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stub required")
	}

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "gh")
	script := "#!/bin/sh\necho gh-cli-token\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", stubDir)

	// Positive: the explicit token wins over everything else.
	t.Setenv("GITHUB_TOKEN", "env-github")
	t.Setenv("GH_TOKEN", "env-gh")
	if got := ResolveAuthToken("explicit"); got != "explicit" {
		t.Errorf("explicit token: got %q, want %q", got, "explicit")
	}

	// GITHUB_TOKEN outranks GH_TOKEN.
	if got := ResolveAuthToken(""); got != "env-github" {
		t.Errorf("GITHUB_TOKEN precedence: got %q, want %q", got, "env-github")
	}

	// GH_TOKEN is used when GITHUB_TOKEN is unset.
	t.Setenv("GITHUB_TOKEN", "")
	if got := ResolveAuthToken(""); got != "env-gh" {
		t.Errorf("GH_TOKEN fallback: got %q, want %q", got, "env-gh")
	}

	// The gh CLI session is the last resort.
	t.Setenv("GH_TOKEN", "")
	if got := ResolveAuthToken(""); got != "gh-cli-token" {
		t.Errorf("gh CLI fallback: got %q, want %q", got, "gh-cli-token")
	}
}

// gh prints update notices on standard error; they must never become part of the token.
func TestResolveAuthTokenContext_Positive_IgnoresStandardError(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stub required")
	}
	stubDir := t.TempDir()
	script := "#!/bin/sh\necho 'A new release of gh is available' >&2\necho gh-cli-token\n"
	if err := os.WriteFile(filepath.Join(stubDir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", stubDir)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if got := ResolveAuthTokenContext(context.Background(), ""); got != "gh-cli-token" {
		t.Errorf("token polluted by standard error: %q", got)
	}
}

func TestResolveAuthToken_Negative_NoSourceAvailable(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stub required")
	}
	t.Setenv("PATH", t.TempDir()) // empty PATH: no gh binary at all
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	if got := ResolveAuthToken(""); got != "" {
		t.Errorf("expected the empty string when no token source exists, got %q", got)
	}
}

func TestResolveAuthTokenContext_Boundary_CancelledContext(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stub required")
	}
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "gh"), []byte("#!/bin/sh\necho should-not-be-used\n"), 0o700); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", stubDir)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ResolveAuthTokenContext(ctx, ""); got != "" {
		t.Errorf("expected the empty string for a cancelled context, got %q", got)
	}

	// Boundary: an explicit token short-circuits before any subprocess is started.
	if got := ResolveAuthTokenContext(ctx, "explicit"); got != "explicit" {
		t.Errorf("explicit token must bypass the gh lookup, got %q", got)
	}
}

// ============================================================================
// WriteFileNoFollow / ReadFileNoFollow (3D)
// ============================================================================

func TestWriteFileNoFollow_Positive_CreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	if err := WriteFileNoFollow(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFileNoFollow failed: %v", err)
	}
	if err := WriteFileNoFollow(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFileNoFollow replace failed: %v", err)
	}

	data, err := ReadFileNoFollow(path)
	if err != nil {
		t.Fatalf("ReadFileNoFollow failed: %v", err)
	}
	if string(data) != "second" {
		t.Errorf("unexpected content %q", string(data))
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if ModeIsProtection() && info.Mode().Perm() != 0o644 {
		t.Errorf("unexpected mode %v", info.Mode().Perm())
	}
}

func TestWriteFileNoFollow_Negative_RefusesSymlinkAndBadPerm(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	err := WriteFileNoFollow(link, []byte("payload"), 0o644)
	if !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("expected ErrSymlinkDestination, got %v", err)
	}
	data, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("read victim: %v", readErr)
	}
	if string(data) != "do not touch" {
		t.Errorf("the write followed the symlink: %q", string(data))
	}

	if _, err := ReadFileNoFollow(link); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("expected ReadFileNoFollow to refuse a symlink, got %v", err)
	}
	if err := WriteFileNoFollow(filepath.Join(dir, "ok.txt"), []byte("x"), 0o666); !errors.Is(err, ErrInsecurePerm) {
		t.Errorf("expected world-writable perm to be refused, got %v", err)
	}
}

func TestWriteFileNoFollow_Boundary_DirectoryAndMissingFile(t *testing.T) {
	dir := t.TempDir()

	if err := WriteFileNoFollow(dir, []byte("x"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("expected a directory destination to be refused, got %v", err)
	}
	if _, err := ReadFileNoFollow(filepath.Join(dir, "absent.txt")); err == nil {
		t.Errorf("expected reading an absent file to fail")
	}
	if err := WriteFileNoFollow(filepath.Join(dir, "fresh.txt"), nil, 0o600); err != nil {
		t.Errorf("expected an empty write to a fresh path to succeed, got %v", err)
	}
	if err := WriteFileNoFollow(filepath.Join(dir, "missing", "ledger.json"), []byte("x"), 0o600); err == nil {
		t.Errorf("expected a write below a missing directory to fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed write must not create its directory, stat err = %v", err)
	}
}

// TestWriteFileNoFollow_Negative_HardLinkedVictimUntouched pins the replace-by-rename
// half of BUG-827: the old in-place truncate wrote through a hard link planted at the
// ledger path onto whatever file it shared an inode with.
func TestWriteFileNoFollow_Negative_HardLinkedVictimUntouched(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	ledger := filepath.Join(dir, "milestones.json")
	if err := os.Link(victim, ledger); err != nil {
		t.Skipf("hard links unsupported on this filesystem: %v", err)
	}
	if err := WriteFileNoFollow(ledger, []byte("ledger"), 0o600); err != nil {
		t.Fatalf("WriteFileNoFollow: %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "do not touch" { // #nosec G304 -- test-local path from t.TempDir
		t.Errorf("victim = (%q, %v), want it untouched", data, err)
	}
	if data, err := ReadFileNoFollow(ledger); err != nil || string(data) != "ledger" {
		t.Errorf("ledger = (%q, %v), want the new contents", data, err)
	}
}

// TestWriteFileNoFollow_Boundary_ReplaceNeverWidensExisting keeps WriteFileSecure's
// ceiling across the switch to rename: a replacement keeps only the bits the old file had
// that perm also grants, and no staged temp file survives.
func TestWriteFileNoFollow_Boundary_ReplaceNeverWidensExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not available on Windows")
	}
	dir := t.TempDir()
	ledger := filepath.Join(dir, "BACKLOG.md")
	if err := os.WriteFile(ledger, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(ledger, 0o640); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}
	if err := WriteFileNoFollow(ledger, []byte("new"), 0o604); err != nil {
		t.Fatalf("WriteFileNoFollow: %v", err)
	}
	info, err := os.Lstat(ledger)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %#o, want 0600 (0640 & 0604)", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only %s in %s, got %v", filepath.Base(ledger), dir, entries)
	}
}

// writeProtectedFixture seeds a ledger with the given mode and restores a removable mode
// before t.TempDir's cleanup, which a read-only file would otherwise fail on Windows.
func writeProtectedFixture(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	ledger := filepath.Join(dir, "BACKLOG.md")
	if err := os.WriteFile(ledger, []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(ledger, mode); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ledger, 0o600); err != nil {
			t.Errorf("restore mode: %v", err)
		}
	})
	return ledger
}

// TestWriteFileNoFollow_Negative_WriteProtectedLedgerRefused keeps the write protection
// the in-place writer honored: a rename needs only a writable directory, so without the
// owner-write check a read-only ledger was replaced silently.
func TestWriteFileNoFollow_Negative_WriteProtectedLedgerRefused(t *testing.T) {
	dir := t.TempDir()
	ledger := writeProtectedFixture(t, dir, 0o400)
	if err := WriteFileNoFollow(ledger, []byte("clobber"), 0o600); !errors.Is(err, os.ErrPermission) {
		t.Errorf("WriteFileNoFollow on a read-only ledger = %v, want os.ErrPermission", err)
	}
	if err := WriteFileConfined(dir, filepath.Base(ledger), []byte("clobber"), 0o600); !errors.Is(err, os.ErrPermission) {
		t.Errorf("WriteFileConfined on a read-only ledger = %v, want os.ErrPermission", err)
	}
	if data, err := os.ReadFile(ledger); err != nil || string(data) != "keep" { // #nosec G304 -- test-local path from t.TempDir
		t.Errorf("ledger = (%q, %v), want it untouched", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("expected only the ledger in %s, got (%v, %v)", dir, entries, err)
	}
}

// TestWriteFileNoFollow_Boundary_OwnerWriteBitAlonePermitsReplace pins the edge of the
// write-protection check: the owner-write bit is the whole test, so a file carrying only
// that bit is still replaced.
func TestWriteFileNoFollow_Boundary_OwnerWriteBitAlonePermitsReplace(t *testing.T) {
	dir := t.TempDir()
	ledger := writeProtectedFixture(t, dir, 0o200)
	if err := WriteFileNoFollow(ledger, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileNoFollow on an owner-writable ledger: %v", err)
	}
	if err := os.Chmod(ledger, 0o600); err != nil {
		t.Fatalf("chmod for read-back: %v", err)
	}
	if data, err := ReadFileNoFollow(ledger); err != nil || string(data) != "new" {
		t.Errorf("ledger = (%q, %v), want the replaced contents", data, err)
	}
}

func TestWriteFileConfined_Positive(t *testing.T) {
	root, _ := confinedFixture(t)
	rel := filepath.Join("alias", "milestones.json")
	if err := WriteFileConfined(root, rel, []byte("first"), 0o600); err != nil {
		t.Fatalf("WriteFileConfined through an in-root link: %v", err)
	}
	if err := WriteFileConfined(root, rel, []byte("second"), 0o600); err != nil {
		t.Fatalf("WriteFileConfined replacing the ledger: %v", err)
	}
	if data, err := ReadFileNoFollow(filepath.Join(root, "inner", "milestones.json")); err != nil || string(data) != "second" {
		t.Errorf("ledger = (%q, %v), want the replaced contents at the link target", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "inner"))
	if err != nil || len(entries) != 1 {
		t.Errorf("expected only the ledger in inner, got (%v, %v)", entries, err)
	}
}

func TestWriteFileConfined_Negative(t *testing.T) {
	root, outside := confinedFixture(t)
	victim := filepath.Join(root, "inner", "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.Symlink("victim.txt", filepath.Join(root, "inner", "ledger.json")); err != nil {
		t.Fatalf("symlink ledger: %v", err)
	}
	cases := []struct {
		rel  string
		perm os.FileMode
		want error
	}{
		{filepath.Join("inner", "ledger.json"), 0o600, ErrSymlinkDestination},
		{filepath.Join("out", "planted.json"), 0o600, ErrPathEscapesRoot},
		{filepath.Join("..", "sibling.json"), 0o600, ErrPathEscapesRoot},
		{filepath.Join(outside, "abs.json"), 0o600, ErrAbsoluteRelPath},
		{"ok.json", 0o666, ErrInsecurePerm},
	}
	for _, tc := range cases {
		if err := WriteFileConfined(root, tc.rel, []byte("clobber"), tc.perm); !errors.Is(err, tc.want) {
			t.Errorf("WriteFileConfined(%q, %#o) = %v, want %v", tc.rel, tc.perm, err, tc.want)
		}
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" { // #nosec G304 -- test-local path from t.TempDir
		t.Errorf("victim = (%q, %v), want it untouched", data, err)
	}
	assertEmptyDir(t, outside)
	if err := WriteFileConfined(root, filepath.Join("missing", "ledger.json"), []byte("x"), 0o600); err == nil {
		t.Errorf("expected a write below a missing directory to fail")
	}
}

// TestWriteFileConfined_Boundary_SwapAfterCheck pins BUG-826's window: a ledger directory
// swapped for an escaping link after ConfinePath's check. The pinned write refuses it; the
// old composition (ConfinePath, then WriteFileNoFollow on the checked path) follows the
// link and lands outside the root, which is why the ledger writers moved to
// WriteFileConfined.
func TestWriteFileConfined_Boundary_SwapAfterCheck(t *testing.T) {
	root, outside := confinedFixture(t)
	if err := WriteFileConfined(root, ".", []byte("x"), 0o600); !errors.Is(err, ErrRootItself) || errors.Is(err, ErrSymlinkDestination) {
		t.Errorf(`WriteFileConfined(root, ".") = %v, want ErrRootItself only`, err)
	}
	if err := os.Symlink(filepath.Join(root, "inner"), filepath.Join(root, "absolute")); err != nil {
		t.Fatalf("symlink absolute: %v", err)
	}
	if err := WriteFileConfined(root, filepath.Join("absolute", "ledger.json"), []byte("x"), 0o600); err == nil {
		t.Errorf("expected an in-root link with an absolute target to be refused by the pinned open")
	}

	absRoot, inside, err := confineBelow(root, filepath.Join("inner", "milestones.json"))
	if err != nil {
		t.Fatalf("confineBelow: %v", err)
	}
	swapForLink(t, filepath.Join(root, "inner"), outside)
	if err := writeConfined(absRoot, inside, []byte("ledger"), 0o600); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("directory swapped for an escaping link = %v, want ErrPathEscapesRoot like the check reports", err)
	}
	assertEmptyDir(t, outside)

	if err := WriteFileNoFollow(filepath.Join(absRoot, inside), []byte("ledger"), 0o600); err != nil {
		t.Fatalf("WriteFileNoFollow on the checked path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "milestones.json")); err != nil {
		t.Errorf("expected the root-less write to follow the swapped link (documented contract), stat err = %v", err)
	}
}
