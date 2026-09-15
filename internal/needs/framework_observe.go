package needs

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const maxFrameworkPackages = 512
const maxFrameworkPackageEntries = 128
const maxFrameworkSourceBytes = 8 << 20

var frameworkModuleCharacters = regexp.MustCompile(`^[A-Za-z0-9._~/-]+$`)
var frameworkModuleHost = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

func observeFramework(ctx context.Context, index *FrameworkIndex) (err error) {
	root, err := contextopt.OpenDirectory(ctx, index.RootPath)
	if err != nil {
		return fmt.Errorf("open selected framework: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	module, err := frameworkModule(ctx, root)
	if err != nil {
		return err
	}
	if module != "" {
		index.Name = module
	}
	contract, found, err := loadFrameworkContract(ctx, root, module)
	if err != nil {
		return err
	}
	if found {
		return observeContractFramework(ctx, index, contract)
	}
	candidates, err := frameworkCandidates()
	if err != nil {
		return err
	}
	total := 0
	for _, relative := range sortedFrameworkPaths(candidates) {
		present, err := observeFrameworkPackage(ctx, index.RootPath, relative, &total)
		if err != nil {
			return fmt.Errorf("inspect framework package %s: %w", relative, err)
		}
		if !present {
			continue
		}
		if module == "" {
			return errors.New("observed framework package requires a root go.mod module identity")
		}
		addObservedPackage(index, relative, candidates[relative])
	}
	return ctx.Err()
}

func frameworkModule(ctx context.Context, root *os.Root) (string, error) {
	raw, err := contextopt.ReadRootSnapshot(ctx, root, "go.mod")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read framework go.mod: %w", err)
	}
	return parseFrameworkModule(raw)
}

func parseFrameworkModule(raw []byte) (string, error) {
	lines := strings.Split(string(raw), "\n")
	if len(lines) > 16384 {
		return "", errors.New("framework go.mod exceeds 16384 lines")
	}
	module := ""
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if module != "" {
			return "", errors.New("framework go.mod requires one unambiguous module directive")
		}
		value, err := frameworkModuleValue(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "module")))
		if err != nil {
			return "", err
		}
		module = value
	}
	return frameworkModuleIdentity(module)
}

func frameworkModuleValue(value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "//") {
		return "", errors.New("framework module directive requires an identity")
	}
	if strings.HasPrefix(value, "\"") {
		quoted, err := strconv.QuotedPrefix(value)
		if err != nil {
			return "", fmt.Errorf("framework module quoting: %w", err)
		}
		rest := strings.TrimSpace(value[len(quoted):])
		if rest != "" && !strings.HasPrefix(rest, "//") {
			return "", errors.New("unexpected text after framework module identity")
		}
		return strconv.Unquote(quoted)
	}
	value, _, _ = strings.Cut(value, "//")
	return strings.TrimSpace(value), nil
}

func frameworkModuleIdentity(module string) (string, error) {
	host, _, _ := strings.Cut(module, "/")
	if !frameworkModuleCharacters.MatchString(module) || !frameworkModuleHost.MatchString(host) || !strings.Contains(host, ".") {
		return "", errors.New("framework go.mod requires a valid module identity")
	}
	for _, part := range strings.Split(module, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.Contains(part, "..") {
			return "", errors.New("framework module identity contains an invalid path segment")
		}
	}
	return module, nil
}

func frameworkCandidates() (map[string][]CapabilityKey, error) {
	if len(CanonicalCatalog) > maxFrameworkPackages {
		return nil, errors.New("framework catalog exceeds package bound")
	}
	result := make(map[string][]CapabilityKey)
	for _, entry := range CanonicalCatalog {
		relative, ok := strings.CutPrefix(catalogFrameworkPackage(entry), defaultFrameworkModule+"/")
		if !ok || entry.Status == StatusGap {
			continue
		}
		if !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != relative {
			return nil, fmt.Errorf("invalid catalog replacement path %q", relative)
		}
		result[relative] = appendUniqueCap(result[relative], entry.Capability)
	}
	return result, nil
}

func sortedFrameworkPaths(candidates map[string][]CapabilityKey) []string {
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func addObservedPackage(index *FrameworkIndex, relative string, capabilities []CapabilityKey) {
	path := index.Name + "/" + relative
	domain, _, _ := strings.Cut(relative, "/")
	pkg := index.Packages[path]
	pkg.ImportPath, pkg.Domain = path, domain
	for _, capability := range capabilities {
		pkg.Capabilities = appendUniqueCap(pkg.Capabilities, capability)
		index.Capabilities[capability] = appendUniqueStr(index.Capabilities[capability], path)
	}
	index.Packages[path] = pkg
}

func observeFrameworkPackage(ctx context.Context, base, relative string, total *int) (present bool, err error) {
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(base, filepath.FromSlash(relative)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	inModule, err := frameworkPackageInModule(ctx, base, relative)
	if err != nil || !inModule {
		return false, err
	}
	entries, err := frameworkPackageEntries(root)
	if err != nil {
		return false, err
	}
	return frameworkPackageHasSource(ctx, root, entries, total)
}

func frameworkPackageHasSource(ctx context.Context, root *os.Root, entries []os.DirEntry, total *int) (bool, error) {
	packageName := ""
	hasDeclarations := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		name, declared, err := frameworkSourcePackage(ctx, root, entry.Name(), total)
		if err != nil {
			return false, err
		}
		if packageName != "" && packageName != name {
			return false, errors.New("framework directory has conflicting Go package names")
		}
		packageName = name
		hasDeclarations = hasDeclarations || declared
	}
	return hasDeclarations && packageName != "main", nil
}

func frameworkPackageInModule(ctx context.Context, base, relative string) (bool, error) {
	parts := strings.Split(relative, "/")
	if len(parts) > maxPathSegments {
		return false, errors.New("framework package exceeds path depth bound")
	}
	current := base
	for _, part := range parts {
		current = filepath.Join(current, part)
		_, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(current, "go.mod"))
		if err != nil || exists {
			return false, err
		}
	}
	return true, nil
}

func frameworkPackageEntries(root *os.Root) (entries []os.DirEntry, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err = directory.ReadDir(maxFrameworkPackageEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxFrameworkPackageEntries {
		return nil, errors.New("framework package exceeds 128 directory entries")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

func frameworkSourcePackage(ctx context.Context, root *os.Root, name string, total *int) (string, bool, error) {
	raw, err := contextopt.ReadRootSnapshot(ctx, root, name)
	if err != nil {
		return "", false, err
	}
	*total += len(raw)
	if *total > maxFrameworkSourceBytes {
		return "", false, errors.New("framework source exceeds 8 MiB aggregate limit")
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, raw, parser.SkipObjectResolution)
	if err != nil {
		return "", false, fmt.Errorf("parse framework source %s: %w", name, err)
	}
	return file.Name.Name, frameworkHasDeclaration(file), nil
}

func frameworkHasDeclaration(file *ast.File) bool {
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			return true
		case *ast.GenDecl:
			if declaration.Tok != token.IMPORT && len(declaration.Specs) > 0 {
				return true
			}
		}
	}
	return false
}
