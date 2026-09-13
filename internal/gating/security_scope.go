package gating

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxSecurityPackages = 20000

// resolveSecurityPackages converts Go's package directory listing to confined
// gosec arguments. It never supplies a recursive filesystem pattern.
func resolveSecurityPackages(repoDir, output string) ([]string, error) {
	if len(output) > 4<<20 {
		return nil, errors.New("security package listing exceeds 4 MiB")
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) > maxSecurityPackages {
		return nil, errors.New("security package listing exceeds 20000 entries")
	}
	root, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve security root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	patterns := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		pattern, err := securityPackagePattern(root, line)
		if err != nil {
			return nil, err
		}
		if seen[pattern] {
			continue
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func securityPackagePattern(root, line string) (string, error) {
	if strings.TrimSpace(line) == "" || !filepath.IsAbs(line) {
		return "", errors.New("go list returned a blank or nonabsolute package directory")
	}
	dir, err := filepath.EvalSymlinks(line)
	if err != nil {
		return "", fmt.Errorf("resolve security package: %w", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("go package directory is outside repository: %s", line)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("go package path is not a directory")
	}
	if rel == "." {
		return ".", nil
	}
	return "./" + filepath.ToSlash(rel), nil
}
