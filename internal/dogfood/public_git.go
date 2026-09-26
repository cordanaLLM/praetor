package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

type publicSource struct{ url, sha string }

var publicSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
var publicGitHubRepository = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9._-]{1,100}$`)

func validatePublicOptions(ctx context.Context, opts *PublicLoopOptions) ([]publicSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := normalizePublicBounds(opts); err != nil {
		return nil, err
	}
	var err error
	if opts.SourceRoot, err = filepath.Abs(opts.SourceRoot); err != nil {
		return nil, err
	}
	if opts.ArtifactDir, err = filepath.Abs(opts.ArtifactDir); err != nil {
		return nil, err
	}
	sources, err := parsePublicSources(opts.Repositories)
	if err != nil {
		return nil, err
	}
	manifest, err := publicManifest(ctx, opts.SourceRoot)
	if err != nil {
		return nil, fmt.Errorf("source manifest: %w", err)
	}
	if _, err := config.ValidateLockfile(ctx, opts.SourceRoot, manifest); err != nil {
		return nil, fmt.Errorf("source lock: %w", err)
	}
	return sources, nil
}

func normalizePublicBounds(opts *PublicLoopOptions) error {
	if opts.SourceRoot == "" || opts.ArtifactDir == "" {
		return errors.New("public loop requires source_root and artifact_dir")
	}
	if len(opts.Repositories) == 0 || len(opts.Repositories) > MaxPublicRepositories {
		return fmt.Errorf("public loop requires 1..%d repositories", MaxPublicRepositories)
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 2
	}
	if opts.MaxAttempts < 2 || opts.MaxAttempts > MaxPublicAttempts {
		return fmt.Errorf("public loop attempts must be 2..%d", MaxPublicAttempts)
	}
	var err error
	opts.InputLimits, err = normalizeInputLimits(opts.InputLimits)
	return err
}

func parsePublicSources(inputs []string) ([]publicSource, error) {
	if len(inputs) > MaxPublicRepositories {
		return nil, fmt.Errorf("public sources exceed %d repositories", MaxPublicRepositories)
	}
	sources := make([]publicSource, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for i := 0; i < len(inputs) && i < MaxPublicRepositories; i++ {
		source, err := parsePublicSource(inputs[i])
		if err != nil {
			return nil, err
		}
		identity := strings.ToLower(source.url)
		if seen[identity] {
			return nil, fmt.Errorf("duplicate public repository %s", source.url)
		}
		seen[identity] = true
		sources = append(sources, source)
	}
	return sources, nil
}

func parsePublicSource(input string) (publicSource, error) {
	if len(input) > 4096 {
		return publicSource{}, ErrInvalidRepoURL
	}
	url, sha, pinned := strings.Cut(input, "#")
	if !canonicalPublicURL(url) || (pinned && !publicSHA.MatchString(sha)) {
		return publicSource{}, fmt.Errorf("%w: expected canonical https://github.com/owner/repo with a lowercase 40- or 64-hex commit pin", ErrInvalidRepoURL)
	}
	if !pinned && !popularPublicURL(url) {
		return publicSource{}, fmt.Errorf("%w: additional public repositories require an explicit immutable commit pin", ErrInvalidRepoURL)
	}
	return publicSource{url: url, sha: sha}, nil
}

// The literal grammar excludes credentials, ports, escapes, query parameters,
// alternate hosts and path aliases before Git sees the input.
func canonicalPublicURL(url string) bool {
	if !publicGitHubRepository.MatchString(url) {
		return false
	}
	repo := url[strings.LastIndexByte(url, '/')+1:]
	return repo != "." && repo != ".." && !strings.HasSuffix(strings.ToLower(repo), ".git")
}

func popularPublicURL(url string) bool {
	for i := 0; i < len(PopularBenchmarks) && i < MaxPublicRepositories; i++ {
		if strings.EqualFold(PopularBenchmarks[i], url) {
			return true
		}
	}
	return false
}

// publicCommandContext supplies only explicit non-secret process settings. Git
// ignores workstation/system config, credential helpers, templates and fsmonitor.
// It does not disable user hooks: these are new clones and Praetor's generated
// hooks are deliberately left inactive by the disposable-clone adoption option.
func publicCommandContext(ctx context.Context, dir string) (context.Context, error) {
	return untrustedCloneContext(ctx, dir, "https")
}

// untrustedCloneContext is the one isolation environment every clone of a repository
// Praetor does not own runs in: the public loop and the ephemeral dogfood sandbox. dir
// holds the scratch HOME and TMPDIR, and protocols is the colon-separated GIT_ALLOW_PROTOCOL
// allow-list for that caller.
//
// The environment replaces the parent's rather than extending it, so a credential helper, an
// agent socket, a GIT_TEMPLATE_DIR or an insteadOf rewrite cannot reach a checkout of
// untrusted content. That also means an authenticated transport has nothing to authenticate
// with, which is why the remote sandbox accepts https only.
//
// GIT_ALLOW_PROTOCOL is what refuses a transport, including a repository handed in as a bare
// path rather than a URL: measured on git 2.55.0, "clone -- <local path>" under a list
// without file fails with "transport 'file' not allowed". protocol.file.allow=never restates
// that policy in configuration, for a git that stops honouring the variable its own
// documentation calls legacy. It cannot tighten the allow-list and it does not win against
// it: the public-loop fixture clones a local path with GIT_ALLOW_PROTOCOL=file while this
// pair says never (installPublicGitFixture in public_test.go).
func untrustedCloneContext(ctx context.Context, dir, protocols string) (context.Context, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return nil, err
	}
	home, tmp := filepath.Join(dir, "home"), filepath.Join(dir, "tmp")
	if err := util.MkdirSecure(home, 0o700); err != nil {
		return nil, err
	}
	if err := util.MkdirSecure(tmp, 0o700); err != nil {
		return nil, err
	}
	env := []string{"HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8", "PATH=" + util.ScrubbedToolPath(git),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=" + protocols,
		"GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=protocol.file.allow", "GIT_CONFIG_VALUE_2=never"}
	return util.WithCommandEnvironment(ctx, env)
}

func clonePublicSource(ctx context.Context, source publicSource, target string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultRemoteTimeout)
	defer cancel()
	output, err := util.RunGit(ctx, "", "clone", "--template=", "--depth", "1", "--single-branch", "--no-checkout", "--", source.url, target)
	if err != nil {
		return "", fmt.Errorf("clone: %w (%s)", err, util.TruncateExcerpt(output, 2048))
	}
	ref := "HEAD"
	if source.sha != "" {
		output, err = util.RunGit(ctx, target, "fetch", "--depth", "1", "--no-tags", "origin", source.sha)
		if err != nil {
			return "", fmt.Errorf("fetch pinned commit: %w (%s)", err, util.TruncateExcerpt(output, 2048))
		}
		ref = "FETCH_HEAD"
	}
	if output, err = util.RunGit(ctx, target, "checkout", "--detach", ref); err != nil {
		return "", fmt.Errorf("checkout: %w (%s)", err, util.TruncateExcerpt(output, 2048))
	}
	output, err = util.RunGit(ctx, target, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve source revision: %w", err)
	}
	sha := strings.TrimSpace(output)
	if !publicSHA.MatchString(sha) || (source.sha != "" && source.sha != sha) {
		return "", errors.New("clone revision does not match the requested immutable source")
	}
	return sha, nil
}
