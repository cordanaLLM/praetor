// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A checkout with core.autocrlf=true converts every text file of the bundle directory to CRLF
// (#313). devcontainer.json is compared after normalisation; Dockerfile.praetor is compared as
// raw bytes and fails, and these tests pin that the failure says a checkout did it, so audit can
// name the .gitattributes rule (Attributes) instead of reporting an edit nobody made.

// readyBundleFixture writes a ready bundle and returns its config path.
func readyBundleFixture(t *testing.T) string {
	t.Helper()
	return writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
}

// rewriteBundleFile replaces the text of one bundle file through edit.
func rewriteBundleFile(t *testing.T, config, name string, edit func(string) string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(config), name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toCRLF(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") }

// expectedOf returns the configuration Verify expects of the bundle at path: what it records.
func expectedOf(t *testing.T, path string) *DevContainer {
	t.Helper()
	_, actual, err := readBootstrapConfig(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

// Positive: the bundle declares the one rule that pins its Dockerfile to LF, and the commands
// that write that file again drop its index entry before the checkout; the bundle as written
// verifies, and a CRLF checkout of devcontainer.json alone still verifies, which is why no rule
// names it.
func TestCheckoutAttributes_Positive(t *testing.T) {
	if got := Attributes(); !slices.Equal(got, []string{".devcontainer/Dockerfile.praetor text eol=lf"}) {
		t.Fatalf("Attributes() = %q", got)
	}
	commands := RecheckoutCommands()
	if len(commands) != 2 || !slices.Equal(commands[0], []string{"rm", "--cached", "--quiet", "--", CheckoutPinnedFile}) ||
		!slices.Equal(commands[1], []string{"checkout", "HEAD", "--", CheckoutPinnedFile}) {
		t.Fatalf("RecheckoutCommands() = %q", commands)
	}
	path := readyBundleFixture(t)
	expected := expectedOf(t, path)
	if err := Verify(t.Context(), path, expected); err != nil {
		t.Fatalf("the bundle as written: %v", err)
	}
	rewriteBundleFile(t, path, "devcontainer.json", toCRLF)
	if err := Verify(t.Context(), path, expected); err != nil {
		t.Fatalf("a CRLF devcontainer.json: %v", err)
	}
}

// Negative: a CRLF checkout of Dockerfile.praetor fails verification as a checkout conversion,
// naming the file; an edited Dockerfile fails as the plain mismatch and is no conversion.
func TestCheckoutAttributes_Negative(t *testing.T) {
	path := readyBundleFixture(t)
	expected := expectedOf(t, path)
	rewriteBundleFile(t, path, bootstrapDockerfile, toCRLF)
	err := Verify(t.Context(), path, expected)
	if !errors.Is(err, ErrCheckoutLineEndings) || !strings.Contains(err.Error(), "bootstrap Dockerfile.praetor differs from its recorded inputs only by line endings") {
		t.Fatalf("a CRLF Dockerfile: %v", err)
	}
	rewriteBundleFile(t, path, bootstrapDockerfile, func(text string) string {
		return strings.ReplaceAll(text, "\r\n", "\n") + "RUN echo operator\n"
	})
	err = Verify(t.Context(), path, expected)
	if err == nil || errors.Is(err, ErrCheckoutLineEndings) || !strings.Contains(err.Error(), "bootstrap Dockerfile differs from its recorded inputs") {
		t.Fatalf("an edited Dockerfile: %v", err)
	}
}

// Negative for the rules' reach: every pattern is the literal path of a file the bundle writes,
// so the block at the tail of .gitattributes cannot claim an operator's file beside the bundle.
// A wildcard would make an image there text, over the repository's own binary rule.
func TestCheckoutAttributes_Negative_NoRuleUsesAWildcard(t *testing.T) {
	rules := Attributes()
	if len(rules) == 0 {
		t.Fatal("the bundle declares no rule")
	}
	for _, rule := range rules {
		pattern, attributes, found := strings.Cut(rule, " ")
		if !found || attributes != "text eol=lf" || strings.ContainsAny(pattern, `*?[\"`) {
			t.Fatalf("rule %q is no literal path pinned to LF", rule)
		}
		if pattern != CheckoutPinnedFile || path.Dir(pattern) != ".devcontainer" || path.Base(pattern) != bootstrapDockerfile {
			t.Fatalf("rule %q names another file than the bundle's Dockerfile", rule)
		}
	}
}

// Boundary: only one consistent line-ending style is a conversion. Mixed endings, a lone
// carriage return, a CRLF file with an edit and an empty file are the plain mismatch; the exact
// render is no error.
func TestCheckoutAttributes_Boundary(t *testing.T) {
	spec := &BootstrapSpec{BaseImage: "base@sha256:" + strings.Repeat("a", 64), BuilderImage: "builder@sha256:" + strings.Repeat("b", 64), ArchiveParts: 1}
	render := renderBootstrapDockerfile(spec)
	if err := verifyBootstrapDockerfile([]byte(render), spec); err != nil {
		t.Fatalf("the exact render: %v", err)
	}
	if err := verifyBootstrapDockerfile([]byte(toCRLF(render)), spec); !errors.Is(err, ErrCheckoutLineEndings) {
		t.Fatalf("a CRLF render: %v", err)
	}
	first := strings.Index(render, "\n")
	for name, text := range map[string]string{
		"mixed endings": render[:first] + "\r\n" + render[first+1:],
		"lone CR":       render + "\r",
		"CRLF and edit": toCRLF(render) + "RUN echo operator\r\n",
		"empty":         "",
	} {
		err := verifyBootstrapDockerfile([]byte(text), spec)
		if err == nil || errors.Is(err, ErrCheckoutLineEndings) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
