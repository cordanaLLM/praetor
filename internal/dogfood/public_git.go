package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

type publicSource struct{ url, sha string }

var publicSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

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
	return nil
}

func parsePublicSources(inputs []string) ([]publicSource, error) {
	sources := make([]publicSource, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for i := 0; i < len(inputs) && i < MaxPublicRepositories; i++ {
		if len(inputs[i]) > 4096 {
			return nil, ErrInvalidRepoURL
		}
		url, sha, pinned := strings.Cut(strings.TrimSpace(inputs[i]), "#")
		if !curatedPublicURL(url) || (pinned && !publicSHA.MatchString(sha)) {
			return nil, fmt.Errorf("%w: expected curated HTTPS URL optionally followed by #<commit SHA>", ErrInvalidRepoURL)
		}
		if seen[url] {
			return nil, fmt.Errorf("duplicate public repository %s", url)
		}
		seen[url] = true
		sources = append(sources, publicSource{url: url, sha: sha})
	}
	return sources, nil
}

func curatedPublicURL(url string) bool {
	switch url {
	case "https://github.com/gin-gonic/gin", "https://github.com/spf13/cobra",
		"https://github.com/pallets/flask", "https://github.com/sveltejs/template",
		"https://github.com/google/googletest", "https://github.com/BurntSushi/ripgrep",
		"https://github.com/fastify/fastify", "https://github.com/spring-projects/spring-petclinic":
		return true
	default:
		return false
	}
}

// publicCommandContext supplies only explicit non-secret process settings. Git
// ignores workstation/system config, credential helpers, templates and fsmonitor.
// It does not disable user hooks: these are new clones and Praetor's generated
// hooks are deliberately left inactive by the disposable-clone adoption option.
func publicCommandContext(ctx context.Context, dir string) (context.Context, error) {
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
	env := []string{"HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8", "PATH=" + filepath.Dir(git) + ":/usr/bin:/bin",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https",
		"GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false"}
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
