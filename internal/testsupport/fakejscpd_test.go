// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: --version prints the version the test asked for, else the default.
func TestRunFakeJscpd_Positive_VersionLine(t *testing.T) {
	var out, errOut bytes.Buffer
	t.Setenv(FakeJscpdVersionEnv, "")
	if code := RunFakeJscpd([]string{"--version"}, "jscpd 9.9.9", &out, &errOut); code != 0 || out.String() != "jscpd 9.9.9\n" {
		t.Fatalf("default version: code %d, output %q", code, out.String())
	}
	out.Reset()
	t.Setenv(FakeJscpdVersionEnv, "jscpd 5.4.1")
	if code := RunFakeJscpd([]string{"--version"}, "jscpd 9.9.9", &out, &errOut); code != 0 || out.String() != "jscpd 5.4.1\n" {
		t.Fatalf("requested version: code %d, output %q", code, out.String())
	}
}

// Positive: a scan writes the canned report, or no duplicates, into the configured output folder.
func TestRunFakeJscpd_Positive_WritesReport(t *testing.T) {
	outDir := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "jscpd.json")
	data, err := json.Marshal(map[string]string{"output": outDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, canned := range []string{"", `{"duplicates":[{"format":"python"}]}`} {
		t.Setenv(FakeJscpdReportEnv, canned)
		var out, errOut bytes.Buffer
		if code := RunFakeJscpd([]string{"-c", cfg, "a.py"}, "jscpd 9.9.9", &out, &errOut); code != 0 {
			t.Fatalf("scan: code %d, stderr %q", code, errOut.String())
		}
		got, err := os.ReadFile(filepath.Join(outDir, "jscpd-report.json"))
		if err != nil {
			t.Fatal(err)
		}
		want := canned
		if want == "" {
			want = `{"duplicates":[]}`
		}
		if string(got) != want {
			t.Fatalf("report %q, want %q", got, want)
		}
	}
}

// Negative: a scan without -c, or with a configuration naming no output folder, fails naming why.
func TestRunFakeJscpd_Negative_RefusesMissingConfig(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "jscpd.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{"missing -c flag": {"a.py"}, "names no output folder": {"-c", empty}}
	for want, args := range cases {
		var out, errOut bytes.Buffer
		if code := RunFakeJscpd(args, "jscpd 9.9.9", &out, &errOut); code == 0 || !strings.Contains(errOut.String(), want) {
			t.Errorf("args %q: code %d, stderr %q, want %q", args, code, errOut.String(), want)
		}
	}
}

// Boundary: the oversized switch writes a report one MiB above the scanner's 16 MiB bound, and
// UseFakeJscpd points the scanner at this test binary.
func TestRunFakeJscpd_Boundary_OversizedReportAndUse(t *testing.T) {
	t.Setenv(FakeJscpdOversizedEnv, "1")
	if got := len(fakeJscpdReport()); got != 17<<20 {
		t.Fatalf("oversized report %d bytes, want %d", got, 17<<20)
	}
	t.Setenv(FakeJscpdEnv, "")
	if _, ran := RunFakeJscpdIfRequested("jscpd 9.9.9"); ran {
		t.Fatal("the fake ran although the test binary was not asked to act as jscpd")
	}
	UseFakeJscpd(t, "jscpd 5.4.2")
	if os.Getenv(FakeJscpdEnv) != "1" || os.Getenv(jscpdBinaryEnv) != os.Args[0] || os.Getenv(FakeJscpdVersionEnv) != "jscpd 5.4.2" {
		t.Fatalf("UseFakeJscpd left %s=%q %s=%q %s=%q", FakeJscpdEnv, os.Getenv(FakeJscpdEnv),
			jscpdBinaryEnv, os.Getenv(jscpdBinaryEnv), FakeJscpdVersionEnv, os.Getenv(FakeJscpdVersionEnv))
	}
}
