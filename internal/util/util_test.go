package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
