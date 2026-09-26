package editor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/needs"
)

const (
	maxWorkspaceFiles       = 4096
	maxLanguageObservations = maxWorkspaceFiles + maxLoopBound + 32
	// lspBinaryName is the file the Praetor language server is built as.
	lspBinaryName = "standards-lsp"
)

// defaultPrivateDirs are the agent state trees a repository keeps out of Git; ignoredWorkspaceDir
// skips the same names while scanning.
var defaultPrivateDirs = []string{".workingdir", ".workingdir2"}

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

// Plan is the immutable capability input shared by every editor renderer. Every renderer
// reads the plan and nothing else: a capability the resolver rejected is omitted from the
// generated file rather than asserted anyway (issue #365).
type Plan struct {
	Editors   []string
	Languages []string
	Commands  []Command
	// Complexity carries the ceilings the repository's own policy resolves to, so an IDE
	// inspection profile cannot contradict what `praetorctl audit` enforces (issue #360).
	Complexity  config.ComplexityPolicy
	Extensions  []string
	LSPPath     string
	PrivateDirs []string
}

// resolvePlan builds the capability plan for an already-normalized, non-empty editors list.
// Normalizing and validating editor ids is SynthesizeContext's job (one behaviour, one
// implementation, HISS-19): resolvePlan trusts editors rather than re-deriving and
// re-validating it from opts.Editors a second time.
func resolvePlan(ctx context.Context, opts Options, editors []string) (Plan, error) {
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
		Complexity: opts.Complexity.WithHISSDefaults(), Extensions: extensions,
		LSPPath: lspPath, PrivateDirs: privateDirs}, nil
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
		// The label is the one every renderer already wrote for this command, so a repository
		// that regenerates keeps the entry it had instead of gaining a second spelling of it.
		commands = append(commands, Command{Label: "Standards: Verify All", Program: "make", Args: []string{"verify-all"}, Group: "test"})
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

// resolveLSPPath returns the workspace-relative Praetor language server only when it is
// actually there. An unset LSPPath falls back to the conventional location under BinaryDir;
// without that fallback the field was unreachable from every CLI caller, so the resolver
// always answered "absent" and the renderers asserted the binary anyway (issue #365).
func resolveLSPPath(opts Options, root string, languages []string) string {
	if !opts.IncludeLSP || !slices.Contains(languages, "go") {
		return ""
	}
	for _, candidate := range lspCandidates(opts) {
		rel := filepath.Clean(candidate)
		if rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		if isExecutableRegularFile(filepath.Join(root, rel)) {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// lspCandidates lists the workspace-relative locations the language server may occupy. An
// explicit LSPPath is the only candidate; otherwise the conventional location under BinaryDir
// is tried under both names the build produces, because the Makefile appends .exe on Windows
// (HISS-21: the capability has to be reachable on every host, not only on Unix).
func lspCandidates(opts Options) []string {
	if explicit := strings.TrimSpace(opts.LSPPath); explicit != "" {
		return []string{explicit}
	}
	dir := defaultedBinaryDir(opts.BinaryDir)
	return []string{filepath.Join(dir, lspBinaryName), filepath.Join(dir, lspBinaryName+".exe")}
}

// defaultedBinaryDir is the single answer to "where does this repository put its binaries".
func defaultedBinaryDir(binDir string) string {
	if strings.TrimSpace(binDir) == "" {
		return "bin"
	}
	return binDir
}

// isExecutableRegularFile reports whether path is a regular file this host can execute.
// Windows carries executability in the file extension rather than in a permission bit, and Go
// reports 0666 or 0444 for every file there, so the Unix bit test alone would answer "not
// executable" for every Windows workspace and silently drop the capability (HISS-21).
func isExecutableRegularFile(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return windowsExecutableExts[strings.ToLower(filepath.Ext(path))]
	}
	return info.Mode().Perm()&0o111 != 0
}

// windowsExecutableExts are the extensions Windows runs directly.
var windowsExecutableExts = map[string]bool{".exe": true, ".bat": true, ".cmd": true, ".com": true}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func normalizePrivateDirs(input []string) []string {
	if input == nil {
		// The default names exactly the private trees the workspace scan already skips, so a
		// watcher exclusion and a language scan cannot disagree about what is private.
		input = defaultPrivateDirs
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
