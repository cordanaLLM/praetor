// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

// CheckoutPinnedFile is the one bundle file whose checkout bytes must not vary by platform, as a
// slash-separated path from the repository root. Verification compares Dockerfile.praetor
// against its render as raw bytes, so a checkout that converts it to CRLF (core.autocrlf=true,
// git's default on Windows) fails on a file nobody edited (#313). Nothing else in the directory
// needs a rule: the base64 source parts carry no line terminator and devcontainer.json is
// compared after normalisation (rendersExactly).
const CheckoutPinnedFile = ".devcontainer/" + bootstrapDockerfile

// Attributes returns the .gitattributes rules a repository holding a bundle needs, in order.
// Adoption writes them into its managed attribute block (internal/adopt/gitattributes.go).
//
// Every pattern is the literal path of a file adoption writes, never a wildcard. The block sits
// at the tail of .gitattributes, where git lets it win, so a wildcard such as .devcontainer/*
// would also claim the repository's own files beside the bundle: an image there would become
// text in spite of the repository's *.png binary rule, and the next git add would rewrite its
// CRLF byte pairs.
func Attributes() []string {
	return []string{CheckoutPinnedFile + " text eol=lf"}
}

// RecheckoutCommands returns the git argument vectors that, run in order at the repository
// root, write CheckoutPinnedFile again under the attributes in force. Dropping the index entry
// first is what makes git write: git checkout alone skips a file whose index entry is
// unchanged, and a file a checkout converted is unchanged to git, so it would stay CRLF.
func RecheckoutCommands() [][]string {
	return [][]string{
		{"rm", "--cached", "--quiet", "--", CheckoutPinnedFile},
		{"checkout", "HEAD", "--", CheckoutPinnedFile},
	}
}

// ErrCheckoutLineEndings marks a bootstrap companion that holds its recorded text in another
// line-ending style: the checkout converted it, nobody edited it. Verification still fails, as
// a CRLF Dockerfile is not the file the specification records; the caller names Attributes and
// RecheckoutCommands as the remedy.
var ErrCheckoutLineEndings = errors.New("the checkout converted its line endings")

// verifyBootstrapDockerfile requires dockerfile to be the exact render of spec. A file that
// differs only by one consistent line-ending style (util.CanonicalTextEquivalent, the rule
// managed assets are compared by) is reported as ErrCheckoutLineEndings; an edit, mixed
// endings and a lone carriage return stay the plain mismatch.
func verifyBootstrapDockerfile(dockerfile []byte, spec *BootstrapSpec) error {
	want := []byte(renderBootstrapDockerfile(spec))
	if bytes.Equal(dockerfile, want) {
		return nil
	}
	if converted, err := util.CanonicalTextEquivalent(dockerfile, want); err == nil && converted {
		return fmt.Errorf("bootstrap %s differs from its recorded inputs only by line endings: %w", bootstrapDockerfile, ErrCheckoutLineEndings)
	}
	return errors.New("bootstrap Dockerfile differs from its recorded inputs; " + bundleRepair)
}
