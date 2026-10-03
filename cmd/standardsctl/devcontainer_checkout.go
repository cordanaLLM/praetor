// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxCheckoutAttributeLines bounds the .gitattributes lines the remedy reads (HISS-02).
	maxCheckoutAttributeLines = 4096
	// maxRecheckoutCommands bounds the git commands the remedy names (HISS-02).
	maxRecheckoutCommands = 8
	// devContainerDeclineStep is the adoption step whose decline keeps the rule out of the block.
	devContainerDeclineStep = "dev-container"
)

// devContainerCheckoutRemedy adds the remedy to a DevContainer verification failure that is a
// checkout's line-ending conversion (devcontainer.ErrCheckoutLineEndings): the .gitattributes
// rule that keeps the bundle's Dockerfile LF (adopt.DevContainerAttributes), and the git
// commands that write the file again under it (recheckoutRemedy). A .gitattributes at rootDir
// without the rule names it as missing and says who writes it: adoption, or the operator where
// manifest declines the dev-container step, since adoption then leaves the rule out. One that
// carries the rule says the working tree predates it or another attribute source overrides it.
// Every other failure, nil included, is returned unchanged.
func devContainerCheckoutRemedy(ctx context.Context, rootDir string, manifest *config.Manifest, err error) error {
	if !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
		return err
	}
	rules := adopt.DevContainerAttributes()
	rule := strings.Join(rules, "; ")
	if checkoutAttributesPresent(ctx, rootDir, rules) {
		return fmt.Errorf("%w; .gitattributes carries %q, so this working tree was checked out before the rule or another "+
			"attribute source overrides it (git check-attr text eol -- %s): %s",
			err, rule, devcontainer.CheckoutPinnedFile, recheckoutRemedy())
	}
	// A decline list that cannot be resolved is reported by the gate that reads it; the remedy
	// then names adoption, which fails on the same list.
	if declined, declineErr := adopt.ManifestArtifactDeclined(manifest, devContainerDeclineStep); declineErr == nil && declined {
		return fmt.Errorf("%w; .gitattributes lacks %q, and adoption.decline lists %s, so 'praetorctl adopt' does not write it: "+
			"add the rule to .gitattributes, commit it, then %s", err, rule, devContainerDeclineStep, recheckoutRemedy())
	}
	return fmt.Errorf("%w; .gitattributes lacks %q: run 'praetorctl adopt', which writes the rule, commit .gitattributes, "+
		"then %s", err, rule, recheckoutRemedy())
}

// recheckoutRemedy names the git commands that write the pinned file again
// (devcontainer.RecheckoutCommands) and why git checkout alone is not one of them: it skips a
// file whose index entry is unchanged, which a converted file is.
func recheckoutRemedy() string {
	commands := devcontainer.RecheckoutCommands()
	quoted := make([]string, 0, len(commands))
	for index := 0; index < len(commands) && index < maxRecheckoutCommands; index++ {
		quoted = append(quoted, "'git "+strings.Join(commands[index], " ")+"'")
	}
	return "write the file again with " + strings.Join(quoted, " and then ") +
		" (git checkout alone leaves a file whose index entry is unchanged as it is)"
}

// checkoutAttributesPresent reports whether the .gitattributes at rootDir holds every rule of
// rules as a line of its own. An unreadable or absent file, or one with mixed line endings,
// holds none: the remedy then names the rule as missing.
func checkoutAttributesPresent(ctx context.Context, rootDir string, rules []string) bool {
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, ".gitattributes"))
	if err != nil || !exists {
		return false
	}
	normalized, _, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return false
	}
	lines := strings.SplitN(normalized, "\n", maxCheckoutAttributeLines+1)
	missing := func(rule string) bool { return !slices.Contains(lines, rule) }
	return len(rules) > 0 && !slices.ContainsFunc(rules, missing)
}
