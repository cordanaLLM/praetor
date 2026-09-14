package editor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/needs"
)

const (
	maxWorkspaceFiles       = 4096
	maxLanguageObservations = maxWorkspaceFiles + maxLoopBound + 32
)

var errWorkspaceScanBound = errors.New("editor language scan exceeds file bound")

// Command is a repository command already selected by a caller or observed at a
// supported repository boundary. Renderers must not invent commands.
type Command struct {
	Label   string   `json:"label"`
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
	Group   string   `json:"group,omitempty"`
}

// ExtensionRecommendation carries a caller-supplied registry observation. The
// renderer filters this input but does not independently prove publication.
type ExtensionRecommendation struct {
	ID       string `json:"id"`
	Registry string `json:"registry"`
	// Verified records that the caller checked ID in Registry.
	Verified bool `json:"verified"`
}

// Plan is the immutable capability input shared by every editor renderer.
type Plan struct {
	Editors     []string
	Languages   []string
	Commands    []Command
	Extensions  []string
	LSPPath     string
	PrivateDirs []string
}

func resolvePlan(ctx context.Context, opts Options) (Plan, error) {
	editors := normalizeEditors(opts.Editors)
	if len(editors) == 0 {
		return Plan{}, errors.New("no valid editors declared for synthesis")
	}
	root := opts.WorkspaceRoot
	if root == "" {
		root = "."
	}
	languages, err := normalizeLanguages(opts.Languages)
	if err != nil {
		return Plan{}, err
	}
	observed, err := detectWorkspaceLanguages(ctx, root)
	if err != nil {
		return Plan{}, err
	}
	languages, err = normalizeLanguages(append(append(languages, observed...), profileLanguages(opts.Archetype)...))
	if err != nil {
		return Plan{}, err
	}
	commands := opts.Commands
	if commands == nil {
		commands, err = detectWorkspaceCommands(ctx, root)
		if err != nil {
			return Plan{}, err
		}
	}
	commands, err = validateCommands(commands)
	if err != nil {
		return Plan{}, err
	}
	extensions := verifiedExtensions(opts.Extensions, opts.ExtensionRegistry)
	lspPath := resolveLSPPath(opts, root, languages)
	privateDirs := normalizePrivateDirs(opts.PrivateDirs)
	return Plan{Editors: editors, Languages: languages, Commands: commands,
		Extensions: extensions, LSPPath: lspPath, PrivateDirs: privateDirs}, nil
}

// DetectWorkspaceLanguages returns bounded observed language capabilities. It
// reuses the needs analyzer registry for project markers and supplements it with
// source/config formats that do not have dependency analyzers.
func DetectWorkspaceLanguages(root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultIOTimeout)
	defer cancel()
	return detectWorkspaceLanguages(ctx, root)
}

func detectWorkspaceLanguages(ctx context.Context, root string) ([]string, error) {
	scan := workspaceLanguageScan{ctx: ctx, root: root, languages: make([]string, 0, 12)}
	for _, analyzer := range needs.DefaultRegistry().DetectAll(root) {
		scan.languages = append(scan.languages, analyzer.Language())
		if analyzer.Language() == "typescript" && isRegularFile(filepath.Join(root, "svelte.config.js")) {
			scan.languages = append(scan.languages, "svelte")
		}
	}
	if err := filepath.WalkDir(root, scan.visit); err != nil {
		return nil, err
	}
	return normalizeLanguages(scan.languages)
}

type workspaceLanguageScan struct {
	ctx       context.Context
	root      string
	seen      int
	languages []string
}

func (scan *workspaceLanguageScan) visit(path string, entry fs.DirEntry, walkErr error) error {
	if err := scan.ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() && path != scan.root && ignoredWorkspaceDir(path, entry.Name()) {
		return fs.SkipDir
	}
	if entry.IsDir() {
		return nil
	}
	scan.seen++
	if scan.seen > maxWorkspaceFiles {
		return errWorkspaceScanBound
	}
	if language := languageForFile(entry.Name()); language != "" {
		scan.languages = append(scan.languages, language)
	}
	return nil
}

func ignoredWorkspaceDir(path, name string) bool {
	switch name {
	case ".git", ".workingdir", ".workingdir2", ".worktrees", "node_modules", "vendor", "dist", "build", "target":
		return true
	default:
		return name == "worktrees" && filepath.Base(filepath.Dir(path)) == ".claude"
	}
}

var extensionLanguages = map[string]string{
	".go": "go", ".rs": "rust", ".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp",
	".py": "python", ".ts": "typescript", ".tsx": "typescript",
	".js": "typescript", ".jsx": "typescript", ".svelte": "svelte",
	".yaml": "yaml", ".yml": "yaml", ".md": "markdown", ".mdx": "markdown",
	".sh": "shell", ".bash": "shell",
}

func languageForFile(name string) string {
	if language := extensionLanguages[strings.ToLower(filepath.Ext(name))]; language != "" {
		return language
	}
	if name == "Makefile" || strings.HasSuffix(name, ".mk") {
		return "make"
	}
	return ""
}

func normalizeLanguages(input []string) ([]string, error) {
	if len(input) > maxLanguageObservations {
		return nil, errors.New("editor language observations exceed bound")
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(input))
	for i := 0; i < len(input); i++ {
		language := strings.ToLower(strings.TrimSpace(input[i]))
		if language == "native" {
			for _, item := range []string{"c", "cpp"} {
				if !seen[item] {
					seen[item] = true
					result = append(result, item)
				}
			}
			continue
		}
		if language != "" && !seen[language] {
			seen[language] = true
			result = append(result, language)
		}
		if len(result) > maxLoopBound {
			return nil, errors.New("editor language capability count exceeds bound")
		}
	}
	slices.Sort(result)
	return result, nil
}

func profileLanguages(profile string) []string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "framework", "app-service", "library-client":
		return []string{"go"}
	case "native-gpu-systems":
		return []string{"c", "cpp", "rust"}
	case "pages-site":
		return []string{"typescript"}
	default:
		return nil
	}
}

func detectWorkspaceCommands(ctx context.Context, root string) ([]Command, error) {
	commands := make([]Command, 0, 3)
	hasVerify, err := hasLiteralMakeTarget(ctx, filepath.Join(root, "Makefile"), "verify-all")
	if err != nil {
		return nil, err
	}
	if hasVerify {
		commands = append(commands, Command{Label: "Verify All", Program: "make", Args: []string{"verify-all"}, Group: "test"})
	}
	return commands, nil
}

func validateCommands(input []Command) ([]Command, error) {
	if len(input) > maxLoopBound {
		return nil, errors.New("editor command count exceeds bound")
	}
	result := make([]Command, 0, len(input))
	for _, command := range input {
		command.Label = strings.TrimSpace(command.Label)
		command.Program = strings.TrimSpace(command.Program)
		if command.Label == "" || command.Program == "" || len(strings.Fields(command.Program)) != 1 || filepath.IsAbs(command.Program) {
			return nil, errors.New("editor command requires a label and a portable executable token")
		}
		result = append(result, command)
	}
	return result, nil
}

func hasLiteralMakeTarget(ctx context.Context, path, target string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read Makefile command capabilities: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		left, right, ok := strings.Cut(line, ":")
		if ok && !strings.HasPrefix(strings.TrimSpace(right), "=") && slices.Contains(strings.Fields(left), target) {
			return true, nil
		}
	}
	return false, nil
}

func verifiedExtensions(input []ExtensionRecommendation, registry string) []string {
	if registry == "" {
		return []string{}
	}
	result := make([]string, 0, len(input))
	for i := 0; i < len(input) && i < maxLoopBound; i++ {
		item := input[i]
		id := strings.TrimSpace(item.ID)
		if item.Verified && id != "" && !strings.ContainsAny(id, " \t\r\n") && item.Registry == registry {
			result = append(result, id)
		}
	}
	return normalizeStrings(result)
}

func resolveLSPPath(opts Options, root string, languages []string) string {
	if !opts.IncludeLSP || !slices.Contains(languages, "go") {
		return ""
	}
	rel := filepath.Clean(opts.LSPPath)
	if rel == "." || !filepath.IsLocal(rel) || !isExecutableRegularFile(filepath.Join(root, rel)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func isExecutableRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func normalizePrivateDirs(input []string) []string {
	if input == nil {
		input = []string{".workingdir"}
	}
	result := make([]string, 0, len(input))
	for i := 0; i < len(input) && i < maxLoopBound; i++ {
		dir := filepath.Clean(strings.TrimSpace(input[i]))
		if filepath.IsLocal(dir) && dir != "." {
			result = append(result, filepath.ToSlash(dir))
		}
	}
	return normalizeStrings(result)
}

func normalizeStrings(input []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(input))
	for i := 0; i < len(input) && i < maxLoopBound; i++ {
		value := strings.TrimSpace(input[i])
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func hasLanguage(plan Plan, language string) bool {
	return slices.Contains(plan.Languages, language)
}
