package devcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

const (
	maxBootstrapFiles       = 4096
	maxBootstrapSourceBytes = 8 * 1024 * 1024
	maxBootstrapPathBytes   = 256
)

type bootstrapSourceFile struct {
	Name string
	Data []byte
}

func captureBootstrapSource(ctx context.Context, root string) ([]bootstrapSourceFile, error) {
	ready, err := bootstrapSourceAvailable(ctx, root)
	if err != nil || !ready {
		return nil, err
	}
	paths, err := bootstrapSourcePaths(ctx, root)
	if err != nil {
		return nil, err
	}
	files := make([]bootstrapSourceFile, 0, len(paths))
	total := 0
	for _, path := range paths {
		data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("bootstrap source %s: %w", path, err)
		}
		total += len(data)
		if total > maxBootstrapSourceBytes {
			return nil, errors.New("bootstrap source exceeds 8 MiB")
		}
		if err := validateBootstrapSourceFile(path, data); err != nil {
			return nil, err
		}
		files = append(files, bootstrapSourceFile{Name: path, Data: data})
	}
	if err := validateBootstrapSourceSet(files); err != nil {
		return nil, err
	}
	return files, nil
}

// bootstrapSourcePathspec, together with the declared Markdown gate assets that
// bootstrapSourcePaths appends, lists exactly what validateBootstrapSourceName accepts. It
// previously also globbed *.s, *.S, *.c, *.h and *.syso, which that validator rejects as
// "non-Go build inputs", so the first native file committed anywhere in the tree --
// including a scanner fixture under testdata -- failed the bootstrap. Asking git for files
// the validator refuses is a contradiction that can only ever produce an error.
//
// The exclusions drop Go's test surface (util.IsGoTestSurface) from every pathspec, the
// appended asset paths included: the recorded Dockerfile
// only runs go build ./cmd/standardsctl, which never reads a _test.go file or a testdata
// directory. Carrying them roughly doubled the compressed archive and pushed it to 96% of
// the four-frame cap, so one added test failed every bootstrap (BUG-985). The glob magic
// makes the leading ** match testdata at the root as well as at any depth.
var bootstrapSourcePathspec = []string{"*.go", "go.mod", "go.sum", "LICENSE", ":(exclude)*_test.go", ":(exclude,glob)**/testdata/**"}

func bootstrapSourcePaths(ctx context.Context, root string) ([]string, error) {
	args := append([]string{"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--"}, bootstrapSourcePathspec...)
	args = append(args, markdownBootstrapAssetPaths()...)
	data, err := runSourceGit(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]bool)
	for _, path := range strings.Split(data, "\x00") {
		if path == "" {
			continue
		}
		if err := validateBootstrapSourceName(path); err != nil {
			return nil, err
		}
		unique[path] = true
		if len(unique) > maxBootstrapFiles {
			return nil, errors.New("bootstrap source exceeds 4096 files")
		}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func runSourceGit(ctx context.Context, root string, args ...string) (string, error) {
	// Disable repository-configured filesystem monitor hooks for read-only inventory.
	command := append([]string{"-c", "core.fsmonitor=false"}, args...)
	result, err := util.RunCommandBytes(ctx, root, "git", contextopt.MaxSourceBytes, command...)
	if err != nil {
		return "", fmt.Errorf("bootstrap source inventory: %w", err)
	}
	return string(result.Stdout), nil
}

func containsBootstrapSource(files []bootstrapSourceFile, name string) bool {
	for _, file := range files {
		if file.Name == name {
			return true
		}
	}
	return false
}

func validateBootstrapSourceName(name string) error {
	if name == "" || len(name) > maxBootstrapPathBytes || filepath.ToSlash(filepath.Clean(name)) != name || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n") {
		return errors.New("invalid bootstrap source path")
	}
	return validateBootstrapSourceKind(name)
}

// validateBootstrapSourceKind admits the module files, the declared Markdown gate assets
// that tools/markdownlint embeds, and non-test Go source: the inputs go build
// ./cmd/standardsctl reads. bootstrapSourcePathspec plus markdownBootstrapAssetPaths asks
// git for exactly these. Go's test surface is refused first, so no asset allowance can admit
// a _test.go file or a testdata member.
func validateBootstrapSourceKind(name string) error {
	switch {
	case util.IsGoTestSurface(name):
		return fmt.Errorf("bootstrap source %s is test-only; go build never reads _test.go files or testdata directories", name)
	case name == "go.mod", name == "go.sum", name == "LICENSE", isMarkdownBootstrapAsset(name):
		return nil
	case !strings.HasSuffix(name, ".go"):
		return errors.New("unsupported bootstrap source file; non-Go build inputs require explicit capture support")
	}
	return nil
}

func validateBootstrapSourceFile(name string, data []byte) error {
	if err := validateBootstrapSourceName(name); err != nil {
		return err
	}
	if !strings.HasSuffix(name, ".go") {
		return nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse bootstrap source %s: %w", name, err)
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:embed ") || strings.HasPrefix(comment.Text, "//go:embed\t") {
				if name != markdownassets.Directory+"/assets.go" || comment.Text != markdownBootstrapEmbedDirective() {
					return fmt.Errorf("bootstrap source %s embeds assets; explicit asset capture is required", name)
				}
			}
		}
	}
	return nil
}

func bootstrapSourceDigest(files []bootstrapSourceFile) string {
	h := sha256.New()
	for _, file := range files {
		h.Write([]byte(file.Name))
		h.Write([]byte{0})
		sum := sha256.Sum256(file.Data)
		h.Write(sum[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func bootstrapDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validBootstrapDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	for _, c := range digest[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func bootstrapSourceAvailable(ctx context.Context, root string) (bool, error) {
	if root == "" {
		return false, nil
	}
	directory, err := contextopt.OpenDirectory(ctx, root)
	if err != nil {
		return false, err
	}
	if err := directory.Close(); err != nil {
		return false, err
	}
	module, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !declaresPraetorModule(module) {
		return false, errors.New("bootstrap source must declare the Praetor module")
	}
	return true, nil
}

// validateBootstrapSourceSet is the one check every captured or decoded set passes. It
// re-applies the name rule to each member, so a test-only or otherwise refused path cannot
// enter a set through any caller, whatever produced the list.
func validateBootstrapSourceSet(files []bootstrapSourceFile) error {
	if len(files) > maxBootstrapFiles {
		return errors.New("bootstrap source exceeds 4096 files")
	}
	for _, required := range []string{"cmd/standardsctl/main.go", "go.mod", "go.sum", "LICENSE"} {
		if !containsBootstrapSource(files, required) {
			return errors.New("bootstrap requires CLI sources, go.mod, go.sum and LICENSE")
		}
	}
	for _, file := range files {
		if err := validateBootstrapSourceName(file.Name); err != nil {
			return err
		}
		if file.Name == "go.mod" && !declaresPraetorModule(file.Data) {
			return errors.New("bootstrap archive must declare the Praetor module")
		}
	}
	if containsBootstrapSource(files, markdownassets.Directory+"/assets.go") {
		for _, asset := range markdownBootstrapAssetPaths() {
			if !containsBootstrapSource(files, asset) {
				return fmt.Errorf("bootstrap Markdown asset %s is missing", asset)
			}
		}
	}
	return nil
}

func markdownBootstrapAssetPaths() []string {
	names := markdownassets.Names()
	paths := make([]string, 0, len(names))
	for index := 0; index < len(names) && index < markdownassets.MaxAssets; index++ {
		paths = append(paths, markdownassets.Directory+"/"+names[index])
	}
	return paths
}

func isMarkdownBootstrapAsset(name string) bool {
	for _, candidate := range markdownBootstrapAssetPaths() {
		if name == candidate {
			return true
		}
	}
	return false
}

func markdownBootstrapEmbedDirective() string {
	return "//go:embed " + strings.Join(markdownassets.Names(), " ")
}

func declaresPraetorModule(data []byte) bool {
	count := 0
	valid := false
	lines := strings.Split(string(data), "\n")
	if len(lines) > 4096 {
		return false
	}
	for _, line := range lines {
		directive, _, _ := strings.Cut(line, "//")
		fields := strings.Fields(directive)
		if len(fields) > 0 && fields[0] == "module" {
			count++
			valid = len(fields) == 2 && fields[1] == "github.com/cordanaLLM/praetor"
		}
	}
	return count == 1 && valid
}
