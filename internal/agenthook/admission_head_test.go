// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type headAdmissionCase struct {
	name          string
	terminal      string
	open          bool
	openPage      int
	openFullPages int
	wantQueries   string
	failureState  string
	failureBody   string
	want          string
}

// TestAdmission_GitPushHead_OpenFirst exercises both HTTP drivers through push admission.
func TestAdmission_GitPushHead_OpenFirst(t *testing.T) {
	boundQueries := strings.TrimSuffix(strings.Repeat("open,", forge.HeadPullRequestsMaxPages), ",")
	ceiling := fmt.Sprintf("open pull request head listing for %q exceeded %d pages of %d items", "topic", forge.HeadPullRequestsMaxPages, forge.HeadPullRequestsPerPage)
	cases := []headAdmissionCase{
		{name: "hundred_plus_closed_and_open", terminal: "closed", open: true},
		{name: "open_on_page_two", terminal: "merged", open: true, openPage: 2, openFullPages: 1, wantQueries: "open,open"},
		{name: "open_on_last_page", terminal: "merged", open: true, openPage: forge.HeadPullRequestsMaxPages, openFullPages: forge.HeadPullRequestsMaxPages, wantQueries: boundQueries},
		{name: "open_page_bound", terminal: "merged", openFullPages: forge.HeadPullRequestsMaxPages, want: ceiling, wantQueries: boundQueries},
		{name: "open_exhausted_after_full_page", terminal: "merged", openFullPages: 1, want: "#121 is MERGED", wantQueries: "open,open,closed"},
		{name: "open_exhausted_on_last_page", openFullPages: forge.HeadPullRequestsMaxPages - 1, wantQueries: strings.Repeat("open,", forge.HeadPullRequestsMaxPages) + "closed"},
		{name: "merged_only", terminal: "merged", want: "#121 is MERGED"},
		{name: "closed_only", terminal: "closed", want: "#121 is CLOSED"},
		{name: "merged_preferred_over_newer_closed", terminal: "mixed", want: "#120 is MERGED"},
		{name: "no_pull_request"},
		{name: "open_query_error", failureState: "open", want: "forge pull request check failed"},
		{name: "terminal_query_error", failureState: "closed", want: "forge pull request check failed"},
		{name: "open_network_error", failureState: "open", failureBody: "network", want: "forge pull request check failed"},
		{name: "terminal_network_error", failureState: "closed", failureBody: "network", want: "forge pull request check failed"},
		{name: "open_unparsable", failureState: "open", failureBody: "invalid", want: "forge pull request check failed"},
		{name: "terminal_unparsable", failureState: "closed", failureBody: "invalid", want: "forge pull request check failed"},
	}
	for _, driver := range []string{"github", "forgejo"} {
		for _, tc := range cases {
			t.Run(driver+"/"+tc.name, func(t *testing.T) { checkHeadAdmissionCase(t, driver, tc) })
		}
	}
}

// checkHeadAdmissionCase verifies admission and the bounded open/terminal query sequence.
func checkHeadAdmissionCase(t *testing.T, driver string, tc headAdmissionCase) {
	t.Helper()
	var states []string
	srv := headAdmissionServer(t, driver, tc)
	defer srv.Close()
	client := headAdmissionClient(t, srv, tc, &states)
	reader := headAdmissionReader(driver, srv.URL, client)
	reason := checkPushTargetHead(t.Context(), "git push", PushTarget{Branch: "topic"}, reader)
	if (tc.want == "" && reason != "") || (tc.want != "" && !strings.Contains(reason, tc.want)) {
		t.Errorf("refusal = %q, want %q", reason, tc.want)
	}
	wantStates := "open,closed"
	if tc.open || tc.failureState == "open" {
		wantStates = "open"
	}
	if tc.wantQueries != "" {
		wantStates = tc.wantQueries
	}
	if got := strings.Join(states, ","); got != wantStates {
		t.Errorf("queries = %s, want %s", got, wantStates)
	}
}

// headAdmissionServer serves bounded fixtures and checks sequential open page requests.
func headAdmissionServer(t *testing.T, driver string, tc headAdmissionCase) *httptest.Server {
	t.Helper()
	openPages := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if r.URL.Query().Get("state") == "open" {
			openPages++
			page = openPages
		}
		checkHeadAdmissionQuery(t, driver, r, page)
		if r.URL.Query().Get("state") == tc.failureState {
			if tc.failureBody == "" {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			writeTestBody(t, w, tc.failureBody)
			return
		}
		writeTestBody(t, w, headAdmissionPage(t, r, tc))
	}))
}

type headAdmissionTransport func(*http.Request) (*http.Response, error)

// RoundTrip executes the test transport without accessing a live forge.
func (transport headAdmissionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return transport(r)
}

// headAdmissionClient records query order, verifies deadlines and injects network errors.
func headAdmissionClient(t *testing.T, srv *httptest.Server, tc headAdmissionCase, states *[]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: headAdmissionTransport(func(r *http.Request) (*http.Response, error) {
		state := r.URL.Query().Get("state")
		*states = append(*states, state)
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Error("request lacks a bounded context timeout")
		}
		if state == tc.failureState && tc.failureBody == "network" {
			return nil, errors.New("planted network failure")
		}
		return srv.Client().Transport.RoundTrip(r)
	})}
}

// headAdmissionReader creates the shared reader type with either HTTP driver.
func headAdmissionReader(driver, endpoint string, client *http.Client) PullRequestHeadReader {
	return func(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		if driver == "github" {
			d := forge.NewGitHubDriver("stub", endpoint)
			d.HTTPClient = client
			d.SetRepository("acme", "widgets")
			return d.ListPullRequestsByHead(ctx, target.Branch)
		}
		d := forge.NewGiteaDriver("stub", endpoint)
		d.HTTPClient = client
		d.SetRepository("acme", "widgets")
		return d.ListPullRequestsByHead(ctx, target.Branch)
	}
}

// checkHeadAdmissionQuery checks head filtering, page bounds and newest-first terminal sorting.
func checkHeadAdmissionQuery(t *testing.T, driver string, r *http.Request, page int) {
	t.Helper()
	q := r.URL.Query()
	head, limit := "topic", "limit"
	if driver == "github" {
		head, limit = "acme:topic", "per_page"
	}
	if got := q.Get("head") + "/" + q.Get(limit) + "/" + q.Get("page"); got != head+"/20/"+strconv.Itoa(page) {
		t.Errorf("unexpected head/page query: %s", r.URL.RawQuery)
	}
	if q.Get("state") != "closed" {
		return
	}
	if driver == "github" {
		if q.Get("sort") != "created" || q.Get("direction") != "desc" {
			t.Errorf("terminal order: %s", r.URL.RawQuery)
		}
	} else if q.Has("sort") || q.Has("direction") {
		t.Errorf("terminal order: %s", r.URL.RawQuery)
	}
}

// headAdmissionPage models 121 terminal pulls followed by one open pull in an all-state walk.
func headAdmissionPage(t *testing.T, r *http.Request, tc headAdmissionCase) string {
	t.Helper()
	state := r.URL.Query().Get("state")
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		t.Fatalf("parse requested page: %v", err)
	}
	if state == "open" {
		return headAdmissionOpenPage(page, tc)
	}
	if tc.terminal == "" {
		return `[]`
	}
	return headAdmissionTerminalPage(page, tc)
}

// headAdmissionOpenPage places the target on its requested page behind other repository heads.
func headAdmissionOpenPage(page int, tc headAdmissionCase) string {
	openHead := `{"number":122,"state":"open","head":{"ref":"topic","repo":{"full_name":"acme/widgets"}}}`
	if page <= tc.openFullPages {
		items := make([]string, forge.HeadPullRequestsPerPage)
		for i := 0; i < forge.HeadPullRequestsPerPage; i++ {
			items[i] = fmt.Sprintf(`{"number":%d,"state":"open","head":{"ref":"topic","repo":{"full_name":"fork/widgets"}}}`, page*forge.HeadPullRequestsPerPage+i+200)
		}
		if tc.open && page == tc.openPage {
			items[len(items)-1] = openHead
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	if tc.open && (tc.openPage == 0 || page == tc.openPage) {
		return "[" + openHead + "]"
	}
	return `[]`
}

// headAdmissionTerminalPage bounds the fixture to one page of its 121 terminal pulls.
func headAdmissionTerminalPage(page int, tc headAdmissionCase) string {
	items := make([]string, 0, forge.HeadPullRequestsPerPage)
	for i := 0; i < forge.HeadPullRequestsPerPage; i++ {
		number := 121 - (page-1)*forge.HeadPullRequestsPerPage - i
		if number <= 0 {
			if tc.open {
				items = append(items, `{"number":122,"state":"open","head":{"ref":"topic"}}`)
			}
			break
		}
		merged := tc.terminal == "merged" || (tc.terminal == "mixed" && number == 120)
		items = append(items, fmt.Sprintf(`{"number":%d,"state":"closed","merged":%t,"head":{"ref":"topic"}}`, number, merged))
	}
	return "[" + strings.Join(items, ",") + "]"
}
