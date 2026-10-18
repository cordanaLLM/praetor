// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

// StripRendering is the inverse of a workflow renderer that adds settings to a plain workflow:
// read cuts the settings out of text, and StripRendering returns the plain text only when render
// of that plain text with those settings gives text back exactly. An edited line or any other
// deviation returns false, so only the renderer's own output is recognised. The emitted API gate
// (tools/apicompat) and secrets gate (tools/secrets) workflows both use it.
func StripRendering[S any](text string, read func(string) (S, string, bool), render func(string, S) (string, error)) (plain string, ok bool) {
	settings, plain, ok := read(text)
	if !ok {
		return "", false
	}
	again, err := render(plain, settings)
	if err != nil || again != text {
		return "", false
	}
	return plain, true
}
