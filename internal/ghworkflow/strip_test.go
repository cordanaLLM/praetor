// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"errors"
	"strings"
	"testing"
)

const stripMarker = "# settings: "

func readMarker(text string) (string, string, bool) {
	head, plain, found := strings.Cut(text, "\n")
	if !found || !strings.HasPrefix(head, stripMarker) {
		return "", text, true
	}
	return strings.TrimPrefix(head, stripMarker), plain, true
}

func renderMarker(plain, settings string) (string, error) {
	if settings == "refuse" {
		return "", errors.New("refused")
	}
	if settings == "" {
		return plain, nil
	}
	return stripMarker + settings + "\n" + plain, nil
}

// Positive: a rendering strips back to its plain text; text without settings is its own plain text.
func TestStripRendering_Positive_RoundTrip(t *testing.T) {
	rendered, err := renderMarker("on: push\n", "a=1")
	if err != nil {
		t.Fatal(err)
	}
	if plain, ok := StripRendering(rendered, readMarker, renderMarker); !ok || plain != "on: push\n" {
		t.Fatalf("StripRendering(rendering) = %q, %v; want the plain text", plain, ok)
	}
	if plain, ok := StripRendering("on: push\n", readMarker, renderMarker); !ok || plain != "on: push\n" {
		t.Fatalf("StripRendering(plain) = %q, %v; want it unchanged", plain, ok)
	}
}

// Negative: an edited rendering, a reader that refuses and a renderer that fails are not recognised.
func TestStripRendering_Negative_NotTheRenderersOutput(t *testing.T) {
	if _, ok := StripRendering("# settings: a=1\non: push\n# edited\n", readMarker, func(plain, s string) (string, error) {
		return renderMarker(strings.TrimSuffix(plain, "# edited\n"), s)
	}); ok {
		t.Fatal("an edited rendering was recognised")
	}
	refuse := func(string) (string, string, bool) { return "", "", false }
	if _, ok := StripRendering("x", refuse, renderMarker); ok {
		t.Fatal("text the reader refuses was recognised")
	}
	if _, ok := StripRendering("# settings: refuse\non: push\n", readMarker, renderMarker); ok {
		t.Fatal("text the renderer refuses was recognised")
	}
}

// Boundary: empty text with no settings round-trips to empty text.
func TestStripRendering_Boundary_EmptyText(t *testing.T) {
	if plain, ok := StripRendering("", readMarker, renderMarker); !ok || plain != "" {
		t.Fatalf("StripRendering(\"\") = %q, %v; want empty and ok", plain, ok)
	}
}
