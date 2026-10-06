// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The scratch module of the integration test: it requires golang.org/x/text v0.3.7 and calls
// language.ParseAcceptLanguage, which GO-2022-1059 names. The go.sum lines are the checksums the
// Go checksum database records for that version.
const (
	scratchGoMod = "module example.com/scratch\n\ngo 1.22\n\nrequire golang.org/x/text v0.3.7\n"
	scratchGoSum = "golang.org/x/text v0.3.7 h1:olpwvP2KacW1ZWvsR7uQhoyTYvKAupfQrRGBFM352Gk=\n" +
		"golang.org/x/text v0.3.7/go.mod h1:u+2+/6zg+i71rQMx5EYifcz6MCKuco9NR6JIITiCfzQ=\n"
	scratchMain = "package main\n\nimport (\n\t\"fmt\"\n\n\t\"golang.org/x/text/language\"\n)\n\n" +
		"func main() {\n\ttags, _, err := language.ParseAcceptLanguage(\"en-US,en;q=0.9\")\n\tfmt.Println(tags, err)\n}\n"
	// scratchFetchTimeout bounds fetching golang.org/x/text for the scratch module.
	scratchFetchTimeout = 2 * time.Minute
)

// Negative (planted defect), against the real scanner: govulncheck from PATH, the way CI installs
// it from tools/go/go.mod, scans a scratch module that calls a known-vulnerable symbol, and the
// gate fails it. The scan reads testdata/vulndb, so no vulnerability database is fetched; the
// module itself must be in the module cache or fetchable. The test skips, saying why, where
// govulncheck is not installed or the module cannot be fetched (offline).
func TestCheck_Integration_RealScannerFailsACalledSymbol(t *testing.T) {
	scanner, err := exec.LookPath(DefaultScanner)
	if err != nil {
		t.Skipf("govulncheck is not on PATH (%v); install it with "+
			"go install -modfile=tools/go/go.mod golang.org/x/vuln/cmd/govulncheck", err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOTOOLCHAIN", "local")
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": scratchGoMod, "go.sum": scratchGoSum, "main.go": scratchMain} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fetchCtx, cancel := context.WithTimeout(t.Context(), scratchFetchTimeout)
	defer cancel()
	if out, err := util.RunCommand(fetchCtx, dir, "go", "mod", "download", "golang.org/x/text"); err != nil {
		t.Skipf("golang.org/x/text v0.3.7 cannot be fetched for the scratch module (offline?): %v: %s", err, out)
	}
	report, err := Check(t.Context(), Options{Dir: dir, Scanner: []string{scanner, "-db", vulnDBURL(t)}, Run: util.RunCommand})
	mustFail(t, report, err, "GO-2022-1059: golang.org/x/text/language.ParseAcceptLanguage is called (fixed in v0.3.8)")
	if !strings.HasPrefix(report.Scanner, "govulncheck ") {
		t.Errorf("scanner = %q", report.Scanner)
	}
}

// vulnDBURL is the file URL of testdata/vulndb, a vulnerability database in the layout
// govulncheck's -db flag reads (https://go.dev/security/vuln/database#api).
func vulnDBURL(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "vulndb"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(abs)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // a Windows drive path, C:/...
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
