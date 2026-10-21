// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseByTagAuthentication(t *testing.T) {
	for _, token := range []string{"", "test-token"} {
		t.Run(fmt.Sprintf("authenticated=%t", token != ""), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkReleaseRequest(t, r, token)
				if _, err := fmt.Fprint(w, `{"tag_name":"release/v1","assets":[]}`); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(srv.Close)
			d := NewGitHubDriver(token, srv.URL)
			d.SetRepository("acme", "widgets")
			if _, err := d.ReleaseByTag(t.Context(), "release/v1"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkReleaseRequest(t *testing.T, r *http.Request, token string) {
	t.Helper()
	want := ""
	if token != "" {
		want = "Bearer " + token
	}
	if r.Header.Get("Authorization") != want || len(r.Header.Values("Authorization")) > 1 {
		t.Error("authorization header mismatch")
	}
	if r.URL.EscapedPath() != "/repos/acme/widgets/releases/tags/release%2Fv1" {
		t.Errorf("path %s", r.URL.EscapedPath())
	}
}

func TestReleaseByTagRejectsInvalidMetadata(t *testing.T) {
	cases := []struct{ body, want string }{
		{`null`, "matching published release"},
		{`{"tag_name":"v1","draft":true}`, "published release"},
		{`{"tag_name":"other"}`, "matching"},
		{`{`, "decode metadata"},
		{`{"tag_name":"v1","assets":[` + strings.Repeat(`{},`, MaxReleaseAssets) + `{}]}`, "asset count"},
		{strings.Repeat(" ", maxReleaseMetadataBytes+1), "metadata exceeds"},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].want, func(t *testing.T) {
			c := cases[i]
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := fmt.Fprint(w, c.body); err != nil {
					t.Log(err)
				}
			}))
			t.Cleanup(srv.Close)
			d := NewGitHubDriver("", srv.URL)
			d.SetRepository("acme", "widgets")
			if _, err := d.ReleaseByTag(t.Context(), "v1"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want %s", err, c.want)
			}
		})
	}
}

type observedBody struct {
	io.ReadCloser
	read int64
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	return n, err
}

type releaseTransport struct {
	base http.RoundTripper
	body *observedBody
}

func (tr *releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, ok := r.Context().Deadline(); !ok {
		return nil, fmt.Errorf("request missing context deadline")
	}
	resp, err := tr.base.RoundTrip(r)
	if err == nil {
		tr.body = &observedBody{ReadCloser: resp.Body}
		resp.Body = tr.body
	}
	return resp, err
}

func TestStreamReleaseAssetByteBoundary(t *testing.T) {
	cases := []struct {
		name      string
		chunked   bool
		limit     int64
		wantRead  int64
		wantError bool
	}{
		{"known over", false, 3, 0, true},
		{"known exact", false, 6, 6, false},
		{"chunked over", true, 3, 3, true},
		{"chunked under", true, 7, 6, false},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			c := cases[i]
			srv := httptest.NewServer(boundaryAssetHandler(t, c.chunked))
			t.Cleanup(srv.Close)
			d := NewGitHubDriver("", srv.URL)
			d.SetRepository("acme", "widgets")
			tr := &releaseTransport{base: srv.Client().Transport}
			d.HTTPClient.Transport = tr
			n, err := d.StreamReleaseAsset(t.Context(), 1, c.limit, io.Discard)
			if (err != nil) != c.wantError || n != c.wantRead || tr.body.read != c.wantRead {
				t.Fatalf("bytes=%d read=%d error=%v", n, tr.body.read, err)
			}
		})
	}
}

func boundaryAssetHandler(t *testing.T, chunked bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if chunked {
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
			}
		}
		if _, err := fmt.Fprint(w, "abcdef"); err != nil {
			t.Log(err)
		}
	})
}

func TestReleaseRequestsCancelledAndInvalid(t *testing.T) {
	d := NewGitHubDriver("", "http://127.0.0.1:1")
	d.SetRepository("acme", "widgets")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.ReleaseByTag(ctx, "v1"); err == nil {
		t.Fatal("cancelled metadata read passed")
	}
	if _, err := d.StreamReleaseAsset(ctx, 1, 4, io.Discard); err == nil {
		t.Fatal("cancelled download passed")
	}
	if _, err := d.ReleaseByTag(t.Context(), ""); err == nil {
		t.Fatal("empty tag passed")
	}
	if _, err := d.StreamReleaseAsset(t.Context(), 0, 1, io.Discard); err == nil {
		t.Fatal("invalid id passed")
	}
	if _, err := d.StreamReleaseAsset(t.Context(), 1, 0, io.Discard); err == nil {
		t.Fatal("invalid limit passed")
	}
}
