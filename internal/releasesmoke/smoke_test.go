// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package releasesmoke

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type fixtureAsset struct {
	Name string `json:"name"`
	ID   int64  `json:"id"`
	Size int64  `json:"size"`
	Body string `json:"-"`
}

func fixtureDriver(t *testing.T, assets []fixtureAsset, status int) *forge.GitHubDriver {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path == "/repos/acme/widgets/releases/tags/v1" {
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1", "assets": assets}); err != nil {
				t.Error(err)
			}
			return
		}
		serveAsset(t, w, r, assets)
	}))
	t.Cleanup(srv.Close)
	driver := forge.NewGitHubDriver("", srv.URL)
	driver.SetRepository("acme", "widgets")
	return driver
}

func serveAsset(t *testing.T, w http.ResponseWriter, r *http.Request, assets []fixtureAsset) {
	t.Helper()
	for i := 0; i < len(assets); i++ {
		if r.URL.Path != "/repos/acme/widgets/releases/assets/"+strconv.FormatInt(assets[i].ID, 10) {
			continue
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Error("asset accept header missing")
		}
		if _, err := fmt.Fprint(w, assets[i].Body); err != nil {
			t.Error(err)
		}
		return
	}
	t.Errorf("unexpected path: %s", r.URL.Path)
	http.NotFound(w, r)
}

func asset(name, body string, id int64) fixtureAsset {
	return fixtureAsset{Name: name, Body: body, Size: int64(len(body)), ID: id}
}

func sums(name, body string) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(body)), name)
}

func TestPlantedNegatives(t *testing.T) {
	cases := []struct {
		name   string
		assets []fixtureAsset
		want   string
	}{
		{"zero assets", nil, "release v1: zero assets"},
		{"checksum mismatch", []fixtureAsset{asset("SHA256SUMS", sums("app", "good"), 1), asset("app", "evil", 2)}, "app: SHA-256 mismatch"},
		{"listed missing", []fixtureAsset{asset("SHA256SUMS", sums("absent", "good"), 1)}, "absent: listed in SHA256SUMS but missing"},
		{"extra asset", []fixtureAsset{asset("SHA256SUMS", sums("app", "good"), 1), asset("app", "good", 2), asset("extra", "data", 3)}, "extra: missing from SHA256SUMS"},
		{"goreleaser mismatch", []fixtureAsset{asset("checksums.txt", sums("app", "good"), 1), asset("app", "evil", 2)}, "app: SHA-256 mismatch"},
		{"two manifests", []fixtureAsset{asset("SHA256SUMS", sums("app", "good"), 1), asset("app_checksums.txt", sums("app", "good"), 2), asset("app", "good", 3)}, "several checksum manifests"},
		{"empty asset", []fixtureAsset{asset("app", "", 1)}, "app: empty"},
		{"wrong size", []fixtureAsset{{Name: "app", Body: "data", Size: 9, ID: 1}}, "app: size mismatch"},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			c := cases[i]
			_, err := Run(t.Context(), fixtureDriver(t, c.assets, 200), Options{Tag: "v1", MaxBytes: 1024})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want %s", err, c.want)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestCleanTwins(t *testing.T) {
	cases := []struct {
		name   string
		assets []fixtureAsset
		allow  bool
	}{
		{"checksums", []fixtureAsset{asset("SHA256SUMS", sums("app", "good"), 1), asset("app", "good", 2)}, false},
		{"goreleaser manifest and sigstore bundle", []fixtureAsset{asset("checksums.txt", sums("app", "good"), 1), asset("app", "good", 2), asset("checksums.txt.sigstore.json", "bundle", 3)}, false},
		{"named manifest", []fixtureAsset{asset("tool-1.0-SHA256SUMS", sums("tool-1.0.tgz", "good"), 1), asset("tool-1.0.tgz", "good", 2)}, false},
		{"plain assets", []fixtureAsset{asset("app", "good", 1)}, false},
		{"declared assetless", nil, true},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			c := cases[i]
			res, err := Run(t.Context(), fixtureDriver(t, c.assets, 200), Options{Tag: "v1", AllowNoAssets: c.allow, MaxBytes: 1024})
			if err != nil || len(res.Lines) == 0 {
				t.Fatalf("result %+v, error %v", res, err)
			}
		})
	}
}

func TestBundleNotVerified(t *testing.T) {
	assets := []fixtureAsset{asset("SHA256SUMS", sums("app", "good"), 1), asset("app", "good", 2), asset("SHA256SUMS.bundle", "bundle", 3)}
	res, err := Run(t.Context(), fixtureDriver(t, assets, 200), Options{Tag: "v1", MaxBytes: 1024})
	want := "SHA256SUMS.bundle: not verified: no verifier configured"
	if err != nil || !strings.Contains(strings.Join(res.Lines, "\n"), want) {
		t.Fatalf("result %+v, error %v; want %s", res, err, want)
	}
	for i := 0; i < len(res.Lines); i++ {
		if strings.Contains(res.Lines[i], "SHA256SUMS.bundle") && strings.Contains(res.Lines[i], "pass") {
			t.Fatal(res.Lines[i])
		}
	}
}

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		assets []fixtureAsset
		status int
		limit  int64
		want   string
	}{
		{"over total", []fixtureAsset{asset("one", "abc", 1), asset("two", "def", 2)}, 200, 5, "max-bytes"},
		{"missing release", nil, 404, 1024, "release v1: not found"},
		{"invalid limit", nil, 200, -1, "max-bytes"},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			c := cases[i]
			_, err := Run(t.Context(), fixtureDriver(t, c.assets, c.status), Options{Tag: "v1", MaxBytes: c.limit})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want %s", err, c.want)
			}
			t.Logf("refused: %v", err)
		})
	}
}
