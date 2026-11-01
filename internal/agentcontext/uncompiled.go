// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxUncompiledListingBytes  = 64 * 1024
	maxScannedInstructionPaths = 256
	uncompiledProbeTimeout     = 10 * time.Second
)

// ErrUncompiledInstruction marks an uncompiled agent instruction file found outside AGENTS.md.
var ErrUncompiledInstruction = errors.New("agent instruction file outside AGENTS.md")

// UncompiledInstructionFiles returns committable agent instruction files outside AGENTS.md:
// tracked, or untracked and not ignored, matching .github/instructions/*.instructions.md
// or .cursor/rules/*.mdc other than the compiled .cursor/rules/hiss-invariants.mdc.
func UncompiledInstructionFiles(ctx context.Context, repoPath string) ([]string, error) {
	inRepo, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("detect Git work tree for %s: %w", repoPath, err)
	}
	if !inRepo {
		return nil, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, uncompiledProbeTimeout)
	defer cancel()

	result, err := util.RunGitProbe(probeCtx, repoPath, maxUncompiledListingBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
		".github/instructions", ".cursor/rules")
	if err != nil {
		if util.GitAnsweredNotARepository(result, err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read committable instruction files in %s: %w", repoPath, err)
	}

	raw := strings.TrimRight(string(result.Stdout), "\x00")
	if raw == "" {
		return nil, nil
	}

	paths := strings.Split(raw, "\x00")
	var found []string
	for i := 0; i < len(paths) && i < maxScannedInstructionPaths; i++ {
		p := paths[i]
		if isUncompiledInstructionFile(p) {
			found = append(found, p)
		}
	}
	return found, nil
}

// VerifyUncompiledInstructions checks that no committable agent instruction files exist
// outside AGENTS.md, returning an error matching ErrUncompiledInstruction for each.
func VerifyUncompiledInstructions(ctx context.Context, repoPath string) error {
	files, err := UncompiledInstructionFiles(ctx, repoPath)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	errs := make([]error, 0, len(files))
	for i := 0; i < len(files) && i < maxScannedInstructionPaths; i++ {
		errs = append(errs, fmt.Errorf("%w: %s; move its text into AGENTS.md and run compile-context, or remove it",
			ErrUncompiledInstruction, files[i]))
	}
	return errors.Join(errs...)
}

func isUncompiledInstructionFile(p string) bool {
	matchedInstructions, err := path.Match(".github/instructions/*.instructions.md", p)
	if err == nil && matchedInstructions {
		return true
	}
	matchedCursor, err := path.Match(".cursor/rules/*.mdc", p)
	if err == nil && matchedCursor && !isCompiledVendorTarget(p) {
		return true
	}
	return false
}

func isCompiledVendorTarget(rel string) bool {
	for i := 0; i < len(vendorTargets); i++ {
		if vendorTargets[i].path == rel {
			return true
		}
	}
	return false
}
