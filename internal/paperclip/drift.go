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

// ErrHarnessDrift marks a harness file that is not the synthesis for the repository's facts:
// harness.json differs from the harness SynthesizeHarness renders, or rules.md from its rendering.
var ErrHarnessDrift = errors.New("paperclip harness differs from its synthesis")

// CompareGenerated compares .paperclip/harness.json and .paperclip/rules.md under repoPath with
// expected's rendering (MarshalHarness, renderRules) byte for byte, one consistent checkout
// line-ending style folded, as the audit compares every generated text (util.CheckoutTextEqual).
// A rules.md that does not exist passes: adoption keeps a removed one removed (PriorState.Rules).
// It returns the rules.md text it read, "" when absent, for the caller's register gate. A file
// that differs wraps ErrHarnessDrift, naming the file and the first line the synthesis lacks.
func CompareGenerated(ctx context.Context, repoPath string, expected *Harness) (string, error) {
	if ctx == nil || expected == nil {
		return "", errors.New("paperclip: harness comparison requires context and expected harness")
	}
	harnessData, rulesData, rulesExist, err := readHarnessFiles(ctx, repoPath)
	if err != nil {
		return "", err
	}
	want, err := MarshalHarness(expected)
	if err != nil {
		return "", err
	}
	if err := compareFile(harnessFile, harnessData, want); err != nil {
		return "", err
	}
	if !rulesExist {
		return "", nil
	}
	if err := compareFile(rulesFile, rulesData, []byte(renderRules(expected))); err != nil {
		return "", err
	}
	return string(rulesData), nil
}

// compareFile reports how the harness file name, holding actual, differs from expected.
func compareFile(name string, actual, expected []byte) error {
	equal, strict := util.CheckoutTextEqual(actual, expected)
	if equal {
		return nil
	}
	delta := util.LineDeltaOf(string(expected), string(actual), 1)
	first := ""
	if len(delta.RemovedLines) > 0 {
		first = fmt.Sprintf("; first synthesized line it lacks: %q", delta.RemovedLines[0])
	}
	return fmt.Errorf("%w: %s: %d synthesized line(s) missing, %d line(s) not synthesized%s%s",
		ErrHarnessDrift, path.Join(paperclipDir, name), delta.Removed, delta.Added, first, util.ByteExactNote(strict))
}
