// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrHarnessStale marks a harness.json that is unmodified earlier Praetor output (PriorGenerated)
// rather than this release's synthesis for the repository's facts; a plain adopt refreshes it.
var ErrHarnessStale = errors.New("paperclip harness is earlier Praetor output, not this release's synthesis")

// ErrRulesDrift marks a rules.md that is not the rendering of the harness.json beside it.
var ErrRulesDrift = errors.New("paperclip rules.md is not the rendering of harness.json")

// Comparison is how the harness under a repository compares with this release's synthesis.
type Comparison struct {
	// Owned: harness.json is neither the synthesis nor unmodified earlier output. It is the
	// operator's, which adoption keeps byte for byte under --force too (PriorState).
	Owned bool
	// RulesExist: rules.md exists, and Rules holds its text, the rendering of harness.json.
	RulesExist bool
	Rules      string
}

// CompareGenerated judges the harness under repoPath against expected, this release's synthesis
// for the repository's facts, the way the audit does (#321). harness.json passes as expected's
// rendering (MarshalHarness) or as operator-owned, neither that rendering nor unmodified earlier
// output; earlier output wraps ErrHarnessStale, since adoption refreshes it and the audit must
// not accept the stale policy it states. rules.md, when it exists, must be the rendering of
// onDisk, the harness.json as LoadHarnessContext loaded it, so a hand edit wraps ErrRulesDrift
// naming the first rendered line it lacks. Every comparison folds one consistent checkout
// line-ending style, as the audit compares every generated text (util.CheckoutTextEqual); mixed
// endings compare byte for byte. An absent rules.md passes: adoption keeps a removed one removed
// (PriorState.Rules).
func CompareGenerated(ctx context.Context, repoPath string, onDisk, expected *Harness) (Comparison, error) {
	if ctx == nil || onDisk == nil || expected == nil {
		return Comparison{}, errors.New("paperclip: harness comparison requires context, loaded and expected harness")
	}
	harnessData, rulesData, rulesExist, err := readHarnessFiles(ctx, repoPath)
	if err != nil {
		return Comparison{}, err
	}
	owned, err := compareHarnessJSON(harnessData, rulesData, rulesExist, expected)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{Owned: owned, RulesExist: rulesExist}
	if !rulesExist {
		return result, nil
	}
	if err := compareFile(ErrRulesDrift, rulesFile, rulesData, []byte(renderRules(onDisk))); err != nil {
		return Comparison{}, err
	}
	result.Rules = string(rulesData)
	return result, nil
}

// compareHarnessJSON reports whether harnessData, harness.json, is operator-owned: neither
// expected's rendering nor unmodified earlier output (priorState), which wraps ErrHarnessStale.
func compareHarnessJSON(harnessData, rulesData []byte, rulesExist bool, expected *Harness) (bool, error) {
	want, err := MarshalHarness(expected)
	if err != nil {
		return false, err
	}
	if equal, _ := util.CheckoutTextEqual(harnessData, want); equal {
		return false, nil
	}
	prior, err := priorState(harnessData, rulesData, rulesExist, expected)
	if err != nil {
		return false, err
	}
	if !prior.Generated {
		return true, nil
	}
	return false, compareFile(ErrHarnessStale, harnessFile, harnessData, want)
}

// compareFile reports how the harness file name, holding actual, differs from expected, wrapped
// in sentinel; nil when they are equal.
func compareFile(sentinel error, name string, actual, expected []byte) error {
	equal, strict := util.CheckoutTextEqual(actual, expected)
	if equal {
		return nil
	}
	delta := util.LineDeltaOf(string(expected), string(actual), 1)
	first := ""
	if len(delta.RemovedLines) > 0 {
		first = fmt.Sprintf("; first expected line it lacks: %q", delta.RemovedLines[0])
	}
	return fmt.Errorf("%w: %s: %d expected line(s) missing, %d line(s) not expected%s%s",
		sentinel, path.Join(paperclipDir, name), delta.Removed, delta.Added, first, util.ByteExactNote(strict))
}
