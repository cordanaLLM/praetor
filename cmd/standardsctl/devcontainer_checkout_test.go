// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

// A checkout with core.autocrlf=true converts Dockerfile.praetor, which verification compares
// as raw bytes (#313). These tests pin that audit and devcontainer verify name the
// .gitattributes rule as the remedy, missing or present, and leave every other failure alone.

const devContainerRule = ".devcontainer/* text eol=lf"

// convertedBundle generates a ready bundle and converts its Dockerfile to CRLF, as a checkout
// with core.autocrlf=true does. It returns the manifest path, the config path and the root.
func convertedBundle(t *testing.T) (manifest, output, root string) {
	t.Helper()
	manifest, output = cliBootstrapPaths(t)
	if _, err := captureStdout(t, func() error {
		return runDevContainer([]string{"generate", "--config", manifest, "--output", output, "--source-root", cliBootstrapSource(t)})
	}); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(filepath.Dir(output), "Dockerfile.praetor")
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile, []byte(strings.ReplaceAll(string(data), "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest, output, filepath.Dir(manifest)
}

func verifyBundle(t *testing.T, manifest, output string) error {
	t.Helper()
	_, err := captureStdout(t, func() error { return runDevContainer([]string{"verify", "--config", manifest, "--output", output}) })
	return err
}

// Positive: a converted bundle in a repository without the rule fails naming the rule as
// missing and adoption as what writes it; once .gitattributes carries the block adoption
// writes, the same failure says the working tree predates the rule.
func TestDevContainerCheckoutRemedy_Positive(t *testing.T) {
	manifest, output, root := convertedBundle(t)
	err := verifyBundle(t, manifest, output)
	if !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
		t.Fatalf("a converted Dockerfile: %v", err)
	}
	for _, want := range []string{"only by line endings", `.gitattributes lacks "` + devContainerRule + `"`, "run 'praetorctl adopt'", "check the .devcontainer files out again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the remedy lacks %q: %v", want, err)
		}
	}
	writeFixtureFile(t, root, ".gitattributes", "* text=auto\n\n"+adopt.ManagedGitAttributesBlock(adopt.ManagedAttributes(true, false)))
	err = verifyBundle(t, manifest, output)
	for _, want := range []string{`.gitattributes carries "` + devContainerRule + `"`, "checked out before the rule", "git check-attr text eol"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("with the rule present the remedy lacks %q: %v", want, err)
		}
	}
}

// Negative: a failure that is no checkout conversion, and no failure at all, come back
// unchanged; an edited Dockerfile names no rule.
func TestDevContainerCheckoutRemedy_Negative(t *testing.T) {
	root := t.TempDir()
	plain := errors.New("bootstrap Dockerfile differs from its recorded inputs")
	if got := devContainerCheckoutRemedy(t.Context(), root, plain); !errors.Is(got, plain) || got.Error() != plain.Error() {
		t.Fatalf("an unrelated failure was rewritten: %v", got)
	}
	if got := devContainerCheckoutRemedy(t.Context(), root, nil); got != nil {
		t.Fatalf("no failure became %v", got)
	}
	manifest, output, _ := convertedBundle(t)
	dockerfile := filepath.Join(filepath.Dir(output), "Dockerfile.praetor")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyBundle(t, manifest, output)
	if err == nil || errors.Is(err, devcontainer.ErrCheckoutLineEndings) || strings.Contains(err.Error(), ".gitattributes") {
		t.Fatalf("an edited Dockerfile: %v", err)
	}
}

// Boundary: the rule counts as present only as a line of its own. A CRLF .gitattributes
// carries it; a comment naming it, a rule on another pattern, mixed line endings and an absent
// file do not, and the remedy then names the rule as missing.
func TestDevContainerCheckoutRemedy_Boundary(t *testing.T) {
	converted := fmt.Errorf("bootstrap Dockerfile.praetor differs: %w", devcontainer.ErrCheckoutLineEndings)
	for name, test := range map[string]struct {
		attributes string
		present    bool
	}{
		"rule alone":     {devContainerRule + "\n", true},
		"CRLF file":      {"* text=auto\r\n" + devContainerRule + "\r\n", true},
		"no final LF":    {"* text=auto\n" + devContainerRule, true},
		"comment":        {"# " + devContainerRule + "\n", false},
		"other pattern":  {".devcontainer/** text eol=lf\n", false},
		"indented":       {"  " + devContainerRule + "\n", false},
		"mixed endings":  {"* text=auto\r\n" + devContainerRule + "\n", false},
		"empty file":     {"", false},
		"absent file":    {"\x00absent", false},
		"only wildcards": {"* text=auto eol=lf\n", false},
	} {
		root := t.TempDir()
		if test.attributes != "\x00absent" {
			writeFixtureFile(t, root, ".gitattributes", test.attributes)
		}
		err := devContainerCheckoutRemedy(t.Context(), root, converted)
		if !errors.Is(err, devcontainer.ErrCheckoutLineEndings) {
			t.Fatalf("%s: the remedy dropped the cause: %v", name, err)
		}
		if carries := strings.Contains(err.Error(), ".gitattributes carries"); carries != test.present ||
			strings.Contains(err.Error(), ".gitattributes lacks") == test.present {
			t.Fatalf("%s: present = %v, want %v: %v", name, carries, test.present, err)
		}
	}
}

// End to end through the praetorctl process: a fresh adoption writes the rule, and audit of
// that repository names it when the bundle arrives converted, then passes once the checkout
// holds the committed bytes again.
func TestAuditDevContainer_ConvertedCheckoutNamesTheRule(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initGitFixture(t, root)
	if code, out := praetorctl(t, "adopt", "--path", root, "--profile", "planning-artifacts", "--facets", "security:high",
		"--lock-source-root", source); code != 0 {
		t.Fatalf("adopt: exit %d\n%s", code, out)
	}
	block := adopt.ManagedGitAttributesBlock(adopt.ManagedAttributes(true, false))
	if got := readFixtureFile(t, root, ".gitattributes"); got != block || !strings.Contains(block, "\n"+devContainerRule+"\n") {
		t.Fatalf("a fresh adoption wrote .gitattributes %q, want %q", got, block)
	}
	if err := auditDevContainerQuiet(t, root); err != nil {
		t.Fatalf("the adopted bundle: %v", err)
	}
	dockerfile := filepath.Join(".devcontainer", "Dockerfile.praetor")
	committed := readFixtureFile(t, root, dockerfile)
	writeFixtureFile(t, root, dockerfile, strings.ReplaceAll(committed, "\n", "\r\n"))
	err = auditDevContainerQuiet(t, root)
	if !errors.Is(err, devcontainer.ErrCheckoutLineEndings) || !strings.Contains(err.Error(), "[FAIL] DevContainer out of sync with declared standards") ||
		!strings.Contains(err.Error(), `.gitattributes carries "`+devContainerRule+`"`) {
		t.Fatalf("a converted bundle under the rule: %v", err)
	}
	writeFixtureFile(t, root, ".gitattributes", "* text=auto\n")
	if err := auditDevContainerQuiet(t, root); err == nil || !strings.Contains(err.Error(), `.gitattributes lacks "`+devContainerRule+`": run 'praetorctl adopt'`) {
		t.Fatalf("a converted bundle without the rule: %v", err)
	}
	writeFixtureFile(t, root, dockerfile, committed)
	if err := auditDevContainerQuiet(t, root); err != nil {
		t.Fatalf("the committed bytes again: %v", err)
	}
}
