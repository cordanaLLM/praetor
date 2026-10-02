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

// checkoutAttribute keeps every file of the bundle directory LF in the working tree on every
// platform. Verification compares Dockerfile.praetor against its render as raw bytes, so a
// checkout that converts it to CRLF (core.autocrlf=true, git's default on Windows) fails on a
// directory nobody edited (#313). The base64 source parts carry no line terminator and
// devcontainer.json is compared after normalisation (rendersExactly), but the directory is one
// bootstrap unit, so one rule pins it: the rule Praetor's own .gitattributes carries.
const checkoutAttribute = ".devcontainer/* text eol=lf"

// Attributes returns the .gitattributes rules a repository holding a bundle needs, in order.
// Adoption writes them into its managed attribute block (internal/adopt/gitattributes.go).
func Attributes() []string {
	return []string{checkoutAttribute}
}

// ErrCheckoutLineEndings marks a bootstrap companion that holds its recorded text in another
// line-ending style: the checkout converted it, nobody edited it. Verification still fails, as
// a CRLF Dockerfile is not the file the specification records; the caller names Attributes as
// the remedy.
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
