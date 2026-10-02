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
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxCheckoutAttributeLines bounds the .gitattributes lines the remedy reads (HISS-02).
const maxCheckoutAttributeLines = 4096

// devContainerCheckoutRemedy adds the remedy to a DevContainer verification failure that is a
// checkout's line-ending conversion (devcontainer.ErrCheckoutLineEndings): the .gitattributes
// rule that keeps the bundle LF (adopt.DevContainerAttributes), which adoption writes. A
// .gitattributes at rootDir without the rule names it as missing; one that carries it says the
// working tree predates the rule or another attribute source overrides it. Every other
// failure, nil included, is returned unchanged.
func devContainerCheckoutRemedy(ctx context.Context, rootDir string, err error) error {
	if !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
		return err
	}
	rules := adopt.DevContainerAttributes()
	rule := strings.Join(rules, "; ")
	if checkoutAttributesPresent(ctx, rootDir, rules) {
		return fmt.Errorf("%w; .gitattributes carries %q, so this working tree was checked out before the rule or another "+
			"attribute source overrides it (git check-attr text eol -- .devcontainer/Dockerfile.praetor): "+
			"check the .devcontainer files out again", err, rule)
	}
	return fmt.Errorf("%w; .gitattributes lacks %q: run 'praetorctl adopt', which writes the rule, commit .gitattributes "+
		"and check the .devcontainer files out again", err, rule)
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
