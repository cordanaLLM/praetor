package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// writeHermeticHost creates a minimal host repository whose AGENTS.md targets are in
// sync so that RunDogfood exercises the full pipeline without touching the real checkout.
func writeHermeticHost(t *testing.T) string {
	t.Helper()
	host := t.TempDir()
	agents := "# Fixture\n\nMinimal AGENTS.md for dogfood tests.\n"
	if err := os.WriteFile(filepath.Join(host, "AGENTS.md"), []byte(agents), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	return host
}

func TestDogfood_Positive_HomeDirInjection(t *testing.T) {
	host := writeHermeticHost(t)
	home := t.TempDir()
	skillDir := filepath.Join(home, ".claude", "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# demo\n"), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reportPath := filepath.Join(t.TempDir(), "report.json")
	rep, err := RunDogfood(ctx, DogfoodOptions{HostRepoPath: host, HomeDir: home, ReportPath: reportPath})
	if err != nil {
		t.Fatalf("RunDogfood: %v", err)
	}
	if rep.TotalSkillsAudited != 1 {
		t.Errorf("TotalSkillsAudited = %d, want 1 (only the injected home is scanned)", rep.TotalSkillsAudited)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	if util.ModeIsProtection() && info.Mode().Perm()&0o002 != 0 {
		t.Errorf("report is world-writable: %v", info.Mode())
	}
}

func TestDogfood_Negative_MissingTargetsDirAndBadURL(t *testing.T) {
	host := writeHermeticHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := RunDogfood(ctx, DogfoodOptions{HostRepoPath: host, TargetReposDir: missing, SkipWorkstationSkills: true})
	if err == nil || !strings.Contains(err.Error(), "read targets directory") {
		t.Errorf("missing targets dir: got %v, want wrapped read error", err)
	}

	// Option shapes, shell metacharacters and, since the transport allow-list, every
	// transport the sandbox cannot use: a local path, file://, ext::, git:// and ssh://,
	// which has no key, agent or known_hosts once untrustedCloneContext replaces the
	// environment.
	for _, bad := range []string{
		"", "   ", "--upload-pack=touch /tmp/pwned", "https://example.com/a;rm -rf /", "https://example.com/$(id)",
		"/srv/private-repo", "file:///etc", "ext::sh", "git://example.com/repo", "HTTP://example.com/repo",
		"ssh://git@github.com/org/repo.git", "SSH://git@github.com/org/repo.git",
	} {
		if _, vErr := validateRepoURL(bad); !errors.Is(vErr, ErrInvalidRepoURL) {
			t.Errorf("validateRepoURL(%q) = %v, want ErrInvalidRepoURL", bad, vErr)
		}
		if cErr := cloneEphemeralRepo(ctx, bad, t.TempDir(), filepath.Join(t.TempDir(), "clone")); !errors.Is(cErr, ErrInvalidRepoURL) {
			t.Errorf("cloneEphemeralRepo(%q) = %v, want ErrInvalidRepoURL", bad, cErr)
		}
	}
}

// writeCommittedFixture creates a one-commit repository that a clone can resolve without a
// network, and returns its path.
//
// The fixture's own git commands run under testsupport.HermeticGitEnv, the package-wide
// fixture environment: the operator's init.templateDir, commit hooks, signing setting and an
// inherited GIT_DIR or GIT_INDEX_FILE from an enclosing hook cannot shape the fixture or
// reach the enclosing repository, and the identity comes from that one helper rather than
// from per-call -c flags.
func writeCommittedFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(ctx, testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("hermetic fixture environment: %v", err)
	}
	fixture := t.TempDir()
	if out, err := util.RunGit(ctx, fixture, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(fixture, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	if out, err := util.RunGit(ctx, fixture, "add", "a.txt"); err != nil {
		t.Fatalf("add: %v: %s", err, out)
	}
	if out, err := util.RunGit(ctx, fixture, "commit", "-q", "-m", "fixture"); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	return fixture
}

// remoteCloneShim is a recording git stand-in installed first on PATH.
//
// envLog receives the argv and the environment of the first git invocation, so a test can
// assert on what cloneEphemeralRepo actually handed git, and its absence proves git was
// never started. templateDir is a template directory whose post-checkout hook writes marker,
// and the shim offers it to git as GIT_TEMPLATE_DIR: it stands for any template source the
// isolation did not already strip, so a clone that reached checkout without "--template="
// installs and runs that hook.
type remoteCloneShim struct{ envLog, templateDir, marker string }

// installRemoteCloneShim builds the shim and puts it first on PATH. Clone arguments keep
// their shape; only the https target is rewritten to the local fixture, and file transport
// is allowed so the clone can complete without a network.
//
// It is deliberately not installPublicGitFixture (public_test.go): that shim replaces the
// caller's whole argv with a clone command of its own, which would erase the "--template="
// and the depth flags under test here, and it rewrites the origin remote afterwards. The two
// stand-ins answer different questions, so this is not a second implementation of one
// behaviour (HISS-19).
func installRemoteCloneShim(t *testing.T, fixture string) remoteCloneShim {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	if git, err = filepath.Abs(git); err != nil {
		t.Fatalf("resolve git: %v", err)
	}
	aux := t.TempDir()
	shim := remoteCloneShim{
		envLog:      filepath.Join(aux, "git-invocation.txt"),
		templateDir: filepath.Join(aux, "template"),
		marker:      filepath.Join(aux, "template-hook-ran"),
	}
	hooks := filepath.Join(shim.templateDir, "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatalf("mkdir template hooks: %v", err)
	}
	hook := "#!/bin/sh\n: > " + shim.marker + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(hook), 0o700); err != nil {
		t.Fatalf("write template hook: %v", err)
	}
	tools := t.TempDir()
	source := fmt.Sprintf(remoteGitShimSource, strconv.Quote(git), strconv.Quote(fixture),
		strconv.Quote(shim.envLog), strconv.Quote(shim.templateDir))
	testsupport.BuildExecutable(t, tools, "git", source)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+filepath.Dir(git))
	return shim
}

// remoteGitShimSource is built by testsupport.BuildExecutable so the shim runs on every
// platform rather than depending on a shebang.
const remoteGitShimSource = `package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

const realGit = %s
const fixture = %s
const envLog = %s
const templateDir = %s

func main() {
	args := os.Args[1:]
	record := strings.Join(args, "\n") + "\n--env--\n" + strings.Join(os.Environ(), "\n") + "\n"
	// O_EXCL: the clone is the first invocation, and a nested helper must not overwrite it.
	if file, err := os.OpenFile(envLog, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		if _, err := file.WriteString(record); err != nil {
			os.Exit(1)
		}
		if err := file.Close(); err != nil {
			os.Exit(1)
		}
	}
	env := append(os.Environ(), "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+templateDir)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "https://") {
			args[i] = "file://" + fixture
		}
	}
	cmd := exec.Command(realGit, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}
`

// assertIsolatedCloneEnvironment checks the argv and environment the shim recorded: the
// clone must carry an empty template and the scratch HOME, and nothing of the operator's.
func assertIsolatedCloneEnvironment(t *testing.T, shim remoteCloneShim, scratch string) {
	t.Helper()
	recorded, err := os.ReadFile(shim.envLog)
	if err != nil {
		t.Fatalf("git was never invoked for an accepted transport: %v", err)
	}
	for _, want := range []string{
		"--template=", "HOME=" + filepath.Join(scratch, "home"), "TMPDIR=" + filepath.Join(scratch, "tmp"),
		"GIT_ALLOW_PROTOCOL=https", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_KEY_2=protocol.file.allow", "GIT_CONFIG_VALUE_2=never",
	} {
		if !strings.Contains(string(recorded), want) {
			t.Errorf("the clone ran without %q", want)
		}
	}
	// The operator's own environment must not survive into a checkout of untrusted content,
	// and ssh must not be advertised to a sandbox that has no credentials. Both
	// GIT_TEMPLATE_DIR and SSH_AUTH_SOCK are set by the caller before the clone, so neither
	// row can hold merely because the runner never had the variable.
	for _, unwanted := range []string{"GIT_TEMPLATE_DIR=", "SSH_AUTH_SOCK=", "GIT_ALLOW_PROTOCOL=https:ssh"} {
		if strings.Contains(string(recorded), unwanted) {
			t.Errorf("the clone inherited %q", unwanted)
		}
	}
}

// TestCloneEphemeralRepo_Positive_HTTPSCloneCompletesUnderIsolation is the proof that the
// accepted transport still works under the new isolation, and the regression for the
// operator's template hooks landing in the sandbox (praetor#295).
//
// The shim offers git a template directory with a post-checkout hook. "--template=" is what
// refuses it: delete that flag from cloneEphemeralRepo and the hook is installed and runs
// during the checkout of untrusted content, which is exactly the measured defect.
func TestCloneEphemeralRepo_Positive_HTTPSCloneCompletesUnderIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the template hook fixture is a POSIX shell script and the shim rewrites the target to a file:// URL")
	}
	// The fixture and the shim get their own budget: installRemoteCloneShim compiles a Go
	// program through testsupport.BuildExecutable, which on a cold toolchain can spend
	// minutes. Sharing one deadline with the clone made a slow build surface as a context
	// deadline in the clone instead of a result about isolation.
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancelSetup()

	fixture := writeCommittedFixture(t, setupCtx)
	shim := installRemoteCloneShim(t, fixture)
	// The operator's own template directory must not reach git either, and SSH_AUTH_SOCK is
	// set here so the "no agent socket survives" assertion below is not vacuous on a runner
	// that happens to have no ssh agent.
	t.Setenv("GIT_TEMPLATE_DIR", shim.templateDir)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "agent.sock"))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	scratch := t.TempDir()
	target := filepath.Join(t.TempDir(), "clone")
	if err := cloneEphemeralRepo(ctx, "https://github.com/cordanaLLM/absent.git", scratch, target); err != nil {
		t.Fatalf("an accepted transport must still clone under the isolation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "a.txt")); err != nil {
		t.Fatalf("the clone never checked the fixture out: %v", err)
	}
	if _, err := os.Stat(shim.marker); err == nil {
		t.Error("a template post-checkout hook ran during an ephemeral clone")
	}
	if _, err := os.Stat(filepath.Join(target, ".git", "hooks", "post-checkout")); err == nil {
		t.Error("a template hook was installed into the ephemeral sandbox")
	}
	assertIsolatedCloneEnvironment(t, shim, scratch)
}

// TestCloneEphemeralRepo_Negative_RefusedTransportNeverReachesGit is the regression for the
// ephemeral clone resolving whatever string it was handed.
//
// Measured before the fix: a file:// URL cloned, the GIT_TEMPLATE_DIR post-checkout hook was
// installed into the sandbox, and it executed during the clone of untrusted content. The
// shim forwards a file:// target unchanged and allows the file transport, so a URL that
// reached git would clone and run that hook -- the assertions below fail rather than hold
// vacuously if the scheme gate is removed.
func TestCloneEphemeralRepo_Negative_RefusedTransportNeverReachesGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the template hook fixture is a POSIX shell script")
	}
	// Same ordering as the positive test: the shim build has its own budget, so the clone
	// deadline below is the clone's alone.
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancelSetup()

	fixture := writeCommittedFixture(t, setupCtx)
	shim := installRemoteCloneShim(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, refused := range []string{"file://" + fixture, fixture, "ssh://git@github.com/cordanaLLM/praetor.git"} {
		target := filepath.Join(t.TempDir(), "clone")
		if err := cloneEphemeralRepo(ctx, refused, t.TempDir(), target); !errors.Is(err, ErrInvalidRepoURL) {
			t.Errorf("cloneEphemeralRepo(%q) = %v, want ErrInvalidRepoURL", refused, err)
		}
		if _, err := os.Stat(target); err == nil {
			t.Errorf("cloneEphemeralRepo(%q) produced a clone directory", refused)
		}
	}
	if _, err := os.Stat(shim.envLog); err == nil {
		t.Error("a refused target still reached git")
	}
	if _, err := os.Stat(shim.marker); err == nil {
		t.Error("a template post-checkout hook ran for a refused target")
	}
}

func TestDogfood_Boundary_LocalTargetsAndSkipSkills(t *testing.T) {
	host := writeHermeticHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	targets := t.TempDir()
	// A plain file and a directory without .git are skipped; a git repository counts.
	if err := os.WriteFile(filepath.Join(targets, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(targets, "not-a-repo"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	repo := filepath.Join(targets, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := util.RunGit(ctx, repo, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}

	rep, err := RunDogfood(ctx, DogfoodOptions{
		HostRepoPath:          host,
		TargetReposDir:        targets,
		MaxScanTargets:        MaxDogfoodTargets + 1, // above the cap: clamped, not rejected
		SkipWorkstationSkills: true,
	})
	if err != nil {
		t.Fatalf("RunDogfood: %v", err)
	}
	if rep.TargetsEvaluated != 1 || len(rep.TargetResults) != 1 || rep.TargetResults[0].RepoName != "repo" {
		t.Errorf("unexpected target results: %+v", rep.TargetResults)
	}
	if rep.TotalSkillsAudited != 0 {
		t.Errorf("skills audited with SkipWorkstationSkills: %d", rep.TotalSkillsAudited)
	}
}

// TestValidateRepoURL_Boundary_AcceptedSchemeIsNormalised pins what an accepted URL looks
// like when it leaves the validator, not merely that it was accepted.
//
// git matches a URL's scheme against GIT_ALLOW_PROTOCOL case-sensitively. Measured, git
// 2.55.0, env -i, GIT_ALLOW_PROTOCOL=https:
//
//	git clone --template= --depth 1 --single-branch -- HTTPS://127.0.0.1:1/x/y.git dst
//	  -> fatal: transport 'HTTPS' not allowed
//
// while the same command with a lower-case scheme reaches the network. Accepting
// "HTTPS://..." unchanged therefore advertises a transport cloneEphemeralRepo cannot use, so
// the validator rewrites the scheme and this test fails if that rewrite is deleted. The last
// row is the other direction: lower-casing the whole URL instead would break a
// case-sensitive host path (HISS-20).
func TestValidateRepoURL_Boundary_AcceptedSchemeIsNormalised(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" https://github.com/org/repo.git ", "https://github.com/org/repo.git"},
		{"HTTPS://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"HttPs://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https://github.com/Org/CamelRepo.git", "https://github.com/Org/CamelRepo.git"},
	} {
		got, vErr := validateRepoURL(tc.in)
		if vErr != nil {
			t.Errorf("validateRepoURL(%q) rejected an allowed transport: %v", tc.in, vErr)
			continue
		}
		if got != tc.want {
			t.Errorf("validateRepoURL(%q) = %q, want %q: git matches the transport case-sensitively", tc.in, got, tc.want)
		}
	}
}

// TestValidateRepoURL_Negative_SSHIsRefusedWithItsOwnReason pins the ssh:// arm as an arm.
//
// The generic scheme test below it already refuses ssh://, so deleting the ssh branch left
// the whole suite green while the operator lost the one message that says what to do
// instead. This asserts the distinguishing text in both directions: the credential reason
// must be there, and the generic "expected an https:// URL" must not (HISS-20).
func TestValidateRepoURL_Negative_SSHIsRefusedWithItsOwnReason(t *testing.T) {
	for _, sshURL := range []string{
		"ssh://git@github.com/org/repo.git", "SSH://git@github.com/org/repo.git",
	} {
		_, vErr := validateRepoURL(sshURL)
		if !errors.Is(vErr, ErrInvalidRepoURL) {
			t.Fatalf("validateRepoURL(%q) = %v, want ErrInvalidRepoURL", sshURL, vErr)
		}
		if !strings.Contains(vErr.Error(), "the sandbox carries no credentials") {
			t.Errorf("validateRepoURL(%q) = %q, want the ssh-specific reason", sshURL, vErr)
		}
		if strings.Contains(vErr.Error(), "expected an https:// URL") {
			t.Errorf("validateRepoURL(%q) = %q, want the ssh reason rather than the generic scheme refusal", sshURL, vErr)
		}
	}
}
