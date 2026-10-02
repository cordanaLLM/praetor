// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

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
