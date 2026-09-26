package main

import (
	"strings"
	"testing"
)

func runDocsFundingCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("docs", append([]string{"funding"}, args...)) })
}

func TestDocsFundingRendersFromOperatorConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "README.md", "# Fixture\n\n<!-- praetor:funding-support:start -->\n<!-- praetor:funding-support:end -->\n")

	// Boundary: without the document nothing is configured and nothing is published.
	out, err := runDocsFundingCmd(t, dir)
	if err != nil {
		t.Fatalf("unconfigured render: %v\n%s", err, out)
	}
	mustContain(t, out, "Funding: not configured", "skipped (no file or markers): funding file")

	// Negative: a configured document the surfaces do not match yet fails --check without writing.
	writeFixtureFile(t, dir, ".config/operator/funding.yaml", "ko_fi: example\n")
	_, err = runDocsFundingCmd(t, "--check", dir)
	mustErrContain(t, err, "funding surfaces differ")
	if strings.Contains(readFixtureFile(t, dir, "README.md"), "ko-fi.com") {
		t.Fatal("--check wrote the README")
	}

	// Positive: a render writes every surface, after which --check passes.
	if out, err = runDocsFundingCmd(t, dir); err != nil {
		t.Fatalf("configured render: %v\n%s", err, out)
	}
	mustContain(t, readFixtureFile(t, dir, "README.md"), "https://ko-fi.com/example", "## 💖 Support & Sponsorship")
	mustContain(t, readFixtureFile(t, dir, ".github/FUNDING.yml"), "ko_fi: example")
	if out, err = runDocsFundingCmd(t, "--check", dir); err != nil {
		t.Fatalf("check after render: %v\n%s", err, out)
	}

	// Negative: an invalid document and a second positional path are errors.
	writeFixtureFile(t, dir, "alt.yaml", "ko_fi: \"not an account\"\n")
	_, err = runDocsFundingCmd(t, "--config=alt.yaml", dir)
	mustErrContain(t, err, "not a platform account name")
	_, err = runDocsFundingCmd(t, dir, "extra")
	mustErrContain(t, err, "at most one repository path")
}
