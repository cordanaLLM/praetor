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

// observeFramework indexes the checkout at index.RootPath: the packages its own
// capabilities.yaml declares, or else the packages the configured contract declares, each
// kept only when the checkout provides it. A checkout with neither inventory observes no
// package: directory names alone never imply a capability.
func observeFramework(ctx context.Context, index *FrameworkIndex, configured string) (err error) {
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
	name := FrameworkContractFile
	if !found && configured != "" {
		if contract, err = configuredCheckoutContract(ctx, configured, index.Name); err != nil {
			return err
		}
		found, name = true, filepath.Base(configured)
	}
	if !found {
		return ctx.Err()
	}
	if index.Name == "" {
		index.Name = contract.Framework
	}
	return observeContractFramework(ctx, index, contract, name)
}

// configuredCheckoutContract reads the configured contract a checkout without its own
// capabilities.yaml is observed against. A checkout of another module, such as a fork, is
// observed at the contract's package paths rebased onto its own module.
func configuredCheckoutContract(ctx context.Context, path, module string) (*frameworkContract, error) {
	raw, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read framework contract %s: %w", path, err)
	}
	contract, err := parseFrameworkContract(raw, "")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if module == "" || module == contract.Framework {
		return contract, nil
	}
	return contract.rebase(module), nil
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
