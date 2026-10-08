// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

func TestClaimRun_Positive_DispatchesEachCommand(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	d := newTestDesk(f, clock)
	ctx := context.Background()
	steps := []struct {
		cmd    forge.ClaimCommand
		action string
	}{
		{forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "s1", Lane: "agy", Branch: "feat/x"}, "created"},
		{forge.ClaimCommand{Op: forge.ClaimOpStatus, Ref: "acme/widgets#7", Session: "s1", Stage: "review", Note: "n"}, "updated"},
		{forge.ClaimCommand{Op: forge.ClaimOpStatus, Ref: "acme/widgets#7", Session: "s1"}, "read"},
		{forge.ClaimCommand{Op: forge.ClaimOpRelease, Ref: "acme/widgets#7", Session: "s1", Outcome: "landed"}, "released"},
	}
	for _, step := range steps {
		res, err := d.Run(ctx, step.cmd)
		if err != nil || res.Action != step.action {
			t.Fatalf("%s: %+v %v", step.cmd.Op, res, err)
		}
		if !strings.HasPrefix(res.Summary(), step.action+" acme/widgets#7: session s1") {
			t.Fatalf("summary = %q", res.Summary())
		}
	}
}

func TestClaimRun_Negative_BadCommandAndReference(t *testing.T) {
	d := newTestDesk(forgetest.NewClaimFake(), &deskClock{now: claimEpoch})
	for _, cmd := range []forge.ClaimCommand{
		{Op: "steal", Ref: "acme/widgets#7", Session: "s1"},
		{Op: forge.ClaimOpClaim, Ref: "widgets#7", Session: "s1", Lane: "l", Branch: "b"},
		{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "", Lane: "l", Branch: "b"},
		{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "s1", Lane: "", Branch: "b"},
		{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "s1", Lane: "l", Branch: "has space"},
	} {
		if _, err := d.Run(context.Background(), cmd); err == nil {
			t.Fatalf("%+v must be refused", cmd)
		}
	}
}

func TestClaimSummary_Positive_NamesTheTakenOverClaim(t *testing.T) {
	f, clock := forgetest.NewClaimFake(), &deskClock{now: claimEpoch}
	f.Seed(claimAt("old", claimEpoch.Add(-7*time.Hour)), "OWNER")
	res, err := newTestDesk(f, clock).Run(context.Background(), forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "new", Lane: "l", Branch: "b"})
	if err != nil || !strings.Contains(res.Summary(), "replaced the stale claim: claimed by session old") {
		t.Fatalf("summary = %q err %v", res.Summary(), err)
	}
}

// TestNewClaimDesk_Positive_CrossRepositoryOverHTTP runs the real GitHub driver against a
// stand-in server: the claim lands on the repository the reference names, not on a default.
func TestNewClaimDesk_Positive_CrossRepositoryOverHTTP(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	comments := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/other/lib/issues/9":
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 9, "state": "open"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments"):
			_ = json.NewEncoder(w).Encode(comments)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			comments = append(comments, map[string]any{"id": 31, "user": map[string]any{"login": "op"}, "author_association": "OWNER", "body": in["body"]})
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(comments[0])
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "op"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/labels/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_REPOSITORY", "")
	desk := forge.NewClaimDesk("tok", srv.URL, time.Hour)
	res, err := desk.Run(context.Background(), forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "other/lib#9", Session: "s1", Lane: "l", Branch: "b"})
	if err != nil || res.Action != "created" {
		t.Fatalf("claim over http: %+v %v (requests %v)", res, err, paths)
	}
	for _, p := range paths {
		if strings.Contains(p, "/repos/") && !strings.Contains(p, "/repos/other/lib/") {
			t.Fatalf("request left the referenced repository: %s", p)
		}
	}
	// A second session against the same server is refused through the real driver too.
	_, err = desk.Run(context.Background(), forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "other/lib#9", Session: "s2", Lane: "l", Branch: "b"})
	if !errors.Is(err, forge.ErrClaimHeld) {
		t.Fatalf("second session over http: %v", err)
	}
}

func TestNewClaimDesk_Negative_ServerErrorFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := forge.NewClaimDesk("tok", srv.URL, time.Hour).Run(context.Background(),
		forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "other/lib#9", Session: "s1", Lane: "l", Branch: "b"})
	if !errors.Is(err, forge.ErrClaimUnverifiable) {
		t.Fatalf("a 500 must be unverifiable, got %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = forge.NewClaimDesk("tok", srv.URL, time.Hour).Run(cancelled,
		forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "other/lib#9", Session: "s1", Lane: "l", Branch: "b"})
	if !errors.Is(err, forge.ErrClaimUnverifiable) {
		t.Fatalf("a cancelled context must be unverifiable, got %v", err)
	}
}
