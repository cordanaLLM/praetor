// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

const (
	verifyTagSHA     = "0dceb95e7c4cad8cc7422aee3885998f5cab9c79"
	verifyOldSHA     = "ccfb013c15c8afb7bf2b7c028fb74dc5a068cccc"
	verifyGoneSHA    = "26b39f2445243964d4c7385747e4b2144255d441"
	verifyReleaseSHA = "ff45666b9427631e3450c54a1bcbee4d9ff4d7c0"
	verifyGoSHA      = "44694675825211faa026b3c33043df3e48a5fa00"
)

// pinUpstream is a stand-in upstream: tags maps "owner/repo vX" to the commit the tag points
// at (already peeled), extra lists "owner/repo <sha>" commits no tag carries.
type pinUpstream struct {
	tags  map[string]string
	extra map[string]bool
}

// verifyUpstream is the upstream the tests below ask: a registry action whose release tags
// point at their pins, a wait action whose v1.4.0 moved past the pinned v1.3.4 commit, and a
// dispatch action that has never had the pinned commit.
func verifyUpstream() pinUpstream {
	return pinUpstream{
		tags: map[string]string{
			"actions/checkout v7.0.1":        pinnedCheckoutSHA,
			"actions/setup-go v6.0.0":        verifyGoSHA,
			"example/wait-action v1.4.0":     verifyTagSHA,
			"example/wait-action v1":         verifyTagSHA,
			"example/wait-action v1.3.4":     verifyOldSHA,
			"example/dispatch-action v3.0.0": verifyReleaseSHA,
		},
		extra: map[string]bool{"example/untagged-action " + verifyOldSHA: true},
	}
}

// commit answers CommitAt: a refs/tags/ ref by its tag, a SHA when a tag or extra carries it.
func (u pinUpstream) commit(repository, ref string) (string, error) {
	if tag, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
		if commit, found := u.tags[repository+" "+tag]; found {
			return commit, nil
		}
		return "", forge.ErrCommitNotFound
	}
	if u.extra[repository+" "+ref] || len(u.tagsAt(repository, ref)) > 0 {
		return ref, nil
	}
	return "", forge.ErrCommitNotFound
}

// tagsAt answers TagsAt, sorted.
func (u pinUpstream) tagsAt(repository, commit string) []string {
	var names []string
	for key, target := range u.tags {
		if tag, ok := strings.CutPrefix(key, repository+" "); ok && target == commit {
			names = append(names, tag)
		}
	}
	slices.Sort(names)
	return names
}

// fakePinLookup is an ActionPinLookup over a pinUpstream that records every question, fails
// CommitAt for the repositories fail names and every TagsAt with failTags when it is set.
type fakePinLookup struct {
	upstream pinUpstream
	fail     map[string]error
	failTags error
	asked    []string
}

func (f *fakePinLookup) CommitAt(_ context.Context, owner, repo, ref string) (string, error) {
	f.asked = append(f.asked, owner+"/"+repo+" "+ref)
	if err := f.fail[owner+"/"+repo]; err != nil {
		return "", err
	}
	return f.upstream.commit(owner+"/"+repo, ref)
}

func (f *fakePinLookup) TagsAt(_ context.Context, owner, repo, commit string) ([]string, error) {
	f.asked = append(f.asked, owner+"/"+repo+" tags")
	if f.failTags != nil {
		return nil, f.failTags
	}
	return f.upstream.tagsAt(owner+"/"+repo, commit), nil
}

// verifyWorkflow scans a workflow of the given steps for VerifyActionPins.
func verifyWorkflow(t *testing.T, steps ...string) []ActionCandidate {
	t.Helper()
	repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+strings.Join(steps, ""))
	actions, _, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return actions
}

// Positive: a pin whose release tag points at its commit is verified in one question and
// its drift is judged from that release; a comment naming a moving major that has moved on,
// or a release without its "v", is verified by the tag the commit carries; a bare SHA the
// upstream has is unversioned; the same pin in two workflows is asked about once; nothing
// fails.
func TestVerifyActionPinsPositive(t *testing.T) {
	actions := verifyWorkflow(t,
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+" # v7.0.1\n",
		"      - uses: actions/setup-go@"+verifyGoSHA+"  # v6.0.0\n",
		"      - uses: example/untagged-action@"+verifyOldSHA+"\n",
		"      - uses: actions/cache@v6\n",
		"      - uses: example/wait-action@"+verifyOldSHA+" # v1\n",
		"      - uses: example/wait-action@"+verifyOldSHA+" # 1.3.4\n",
		"  again:\n    steps:\n      - uses: actions/checkout@"+pinnedCheckoutSHA+"\t# v7.0.1\n")
	elsewhere := findPagesAction(t, actions, "actions/checkout")
	elsewhere.WorkflowFile = "release.yml"
	actions = append(actions, elsewhere)
	lookup := &fakePinLookup{upstream: verifyUpstream()}
	verified, findings := VerifyActionPins(t.Context(), actions, lookup)
	if len(findings) != 0 || len(verified) != 7 {
		t.Fatalf("findings %+v over %d actions; want none over 7", findings, len(verified))
	}
	want := map[string]string{
		"actions/checkout": "[UP-TO-DATE]", "actions/setup-go": "[DRIFT]",
		"example/untagged-action": "[UNVERSIONED]", "actions/cache": "[UP-TO-DATE]",
		"example/wait-action": "[UP-TO-DATE]",
	}
	for _, action := range verified {
		if got := ActionDriftStatus(action); got != want[action.Action] {
			t.Errorf("%s = %s (%+v), want %s", action.Action, got, action, want[action.Action])
		}
	}
	if checkout := findPagesAction(t, verified, "actions/checkout"); checkout.Pin != PinVerified || !checkout.UpToDate {
		t.Errorf("checkout: want verified and up to date, got %+v", checkout)
	}
	wantAsked := []string{
		"actions/checkout refs/tags/v7.0.1",
		"actions/setup-go refs/tags/v6.0.0",
		"example/untagged-action " + verifyOldSHA,
		"example/wait-action refs/tags/v1", "example/wait-action " + verifyOldSHA, "example/wait-action tags",
		"example/wait-action refs/tags/1.3.4", "example/wait-action " + verifyOldSHA, "example/wait-action tags",
	}
	if !slices.Equal(lookup.asked, wantAsked) {
		t.Errorf("asked %q, want %q", lookup.asked, wantAsked)
	}
}

// Negative: a commit the upstream does not have fails as commit-missing, with or without a
// release comment; a comment whose tag points at another commit, or names no tag, fails as a
// release mismatch that names the tag the pinned commit carries. Each finding names file and
// line, and no refuted pin is up to date.
func TestVerifyActionPinsNegative(t *testing.T) {
	actions := verifyWorkflow(t,
		"      - uses: example/wait-action@"+verifyOldSHA+"  # v1.4.0\n",
		"      - uses: example/dispatch-action@"+verifyGoneSHA+" # v3.0.0\n",
		"      - uses: example/dispatch-action@"+verifyGoneSHA+"\n",
		"      - uses: example/untagged-action@"+verifyOldSHA+" # v9.9.9\n")
	verified, findings := VerifyActionPins(t.Context(), actions, &fakePinLookup{upstream: verifyUpstream()})
	want := []struct{ kind, detail string }{
		{"action-pin-release-mismatch", "tag v1.4.0 points at " + verifyTagSHA + ", not at the pinned commit; the pinned commit carries v1.3.4 in pages.yml:5"},
		{"action-pin-commit-missing", "commit " + verifyGoneSHA + " does not exist in example/dispatch-action; the job stops at setup; tag v3.0.0 points at " + verifyReleaseSHA + " in pages.yml:6"},
		{"action-pin-commit-missing", "does not exist in example/dispatch-action; the job stops at setup in pages.yml:7"},
		{"action-pin-release-mismatch", "example/untagged-action has no tag v9.9.9; no tag points at the pinned commit in pages.yml:8"},
	}
	if len(findings) != len(want) {
		t.Fatalf("findings = %+v, want %d", findings, len(want))
	}
	for index, finding := range findings {
		if finding.Kind != want[index].kind || !strings.HasSuffix(finding.Details, want[index].detail) {
			t.Errorf("finding %d = %+v, want %s ending %q", index, finding, want[index].kind, want[index].detail)
		}
	}
	for _, action := range verified {
		if action.UpToDate || ActionDriftStatus(action) != "[BAD-PIN]" {
			t.Errorf("%s@%s: want a bad pin, got %+v", action.Action, action.CurrentVersion, action)
		}
	}
}

// Boundary: without a lookup every pin is unverified and none is up to date or failing; an
// upstream that cannot be asked halts after its first question, while a repository it does
// not show leaves only its own pin unverified; a tag listing that fails refutes nothing; the
// question budget holds; actions not pinned by SHA pass through unchanged.
func TestVerifyActionPinsBoundary(t *testing.T) {
	actions := verifyWorkflow(t,
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+" # v7.0.1\n",
		"      - uses: example/untagged-action@"+verifyOldSHA+"\n",
		"      - uses: actions/cache@v6\n")
	offline, findings := VerifyActionPins(t.Context(), actions, nil)
	if len(findings) != 0 {
		t.Fatalf("an offline verification failed pins: %+v", findings)
	}
	for _, action := range offline[:2] {
		if action.Pin != PinUnverified || action.UpToDate || action.PinDetail != errNoPinLookup.Error() {
			t.Errorf("offline %s: want unverified, got %+v", action.Action, action)
		}
	}
	if offline[2] != actions[2] {
		t.Errorf("a tag reference changed: %+v -> %+v", actions[2], offline[2])
	}

	limited := &fakePinLookup{upstream: verifyUpstream(), fail: map[string]error{"actions/checkout": errors.New("rate limited")}}
	halted, _ := VerifyActionPins(t.Context(), actions, limited)
	if len(limited.asked) != 1 || halted[1].Pin != PinUnverified || halted[1].PinDetail != "rate limited" {
		t.Errorf("a rate limit did not halt the lookups: asked %q, got %+v", limited.asked, halted[1])
	}
	hidden := &fakePinLookup{upstream: verifyUpstream(), fail: map[string]error{"actions/checkout": forge.ErrRepositoryNotVisible}}
	partial, _ := VerifyActionPins(t.Context(), actions, hidden)
	if partial[0].Pin != PinUnverified || partial[1].Pin != PinUnversioned {
		t.Errorf("a hidden repository halted the lookups: %+v", partial[:2])
	}
	moved := verifyWorkflow(t, "      - uses: example/wait-action@"+verifyOldSHA+"  # v1.4.0\n")
	unlisted, findings := VerifyActionPins(t.Context(), moved, &fakePinLookup{upstream: verifyUpstream(), failTags: errors.New("listing refused")})
	if len(findings) != 0 || unlisted[0].Pin != PinUnverified || !strings.Contains(unlisted[0].PinDetail, "listing refused") {
		t.Errorf("a failed tag listing decided the pin: %+v, %+v", unlisted[0], findings)
	}

	many := make([]ActionCandidate, maxPinLookups+1)
	for index := range many {
		sha := fmt.Sprintf("%040x", index)
		many[index] = ActionCandidate{Action: "example/untagged-action", CurrentVersion: sha, PinnedSHA: sha, Pin: PinUnversioned}
	}
	counted := &fakePinLookup{upstream: verifyUpstream()}
	spent, _ := VerifyActionPins(t.Context(), many, counted)
	if len(counted.asked) != maxPinLookups || spent[maxPinLookups].PinDetail != errPinLookupBudget.Error() {
		t.Errorf("budget: asked %d, last %+v", len(counted.asked), spent[maxPinLookups])
	}
}

// standInGitHub serves the commits and tags endpoints of GitHub's REST API from upstream,
// requiring the bearer token, and returns its URL.
func standInGitHub(t *testing.T, upstream pinUpstream, token string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/"), "/", 4)
		if len(parts) < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		repository := parts[0] + "/" + parts[1]
		var payload any = map[string]string{"message": "No commit found for SHA"}
		status := http.StatusUnprocessableEntity
		switch {
		case parts[2] == "tags":
			var tags []map[string]any
			for key, commit := range upstream.tags {
				if tag, ok := strings.CutPrefix(key, repository+" "); ok {
					tags = append(tags, map[string]any{"name": tag, "commit": map[string]string{"sha": commit}})
				}
			}
			payload, status = tags, http.StatusOK
		case parts[2] == "commits" && len(parts) == 4:
			if commit, err := upstream.commit(repository, parts[3]); err == nil {
				payload, status = map[string]string{"sha": commit}, http.StatusOK
			}
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode stand-in answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Positive, negative and boundary through the audit itself: with a token, the audit asks the
// forge through GitHubActionPinLookup, verifies a current pin and fails the report on a
// missing commit and a moved release; without one, every SHA pin reads unverified, none is
// up to date, and the report does not fail on them.
func TestAuditCodebaseVersionsVerifiesActionPins(t *testing.T) {
	repo := writePagesWorkflow(t, "name: Release\njobs:\n  dispatch:\n    steps:\n"+
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+" # v7.0.1\n"+
		"      - uses: example/wait-action@"+verifyOldSHA+"  # v1.4.0\n"+
		"      - uses: example/dispatch-action@"+verifyGoneSHA+"  # v3.0.0\n"+
		"      - uses: example/dispatch-action@"+verifyGoneSHA+"\n")
	tools := toolchainBin(t)
	t.Setenv("PATH", tools)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "stand-in-token")
	previous := actionPinEndpoint
	actionPinEndpoint = standInGitHub(t, verifyUpstream(), "stand-in-token")
	t.Cleanup(func() { actionPinEndpoint = previous })

	online, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	var statuses, kinds []string
	for _, action := range online.Actions {
		statuses = append(statuses, ActionDriftStatus(action))
	}
	for _, dep := range online.Deprecations {
		kinds = append(kinds, dep.Kind)
	}
	if online.Passed || !slices.Equal(statuses, []string{"[UP-TO-DATE]", "[BAD-PIN]", "[BAD-PIN]", "[BAD-PIN]"}) ||
		!slices.Equal(kinds, []string{"action-pin-release-mismatch", "action-pin-commit-missing", "action-pin-commit-missing"}) {
		t.Fatalf("online audit: passed=%v statuses=%q kinds=%q", online.Passed, statuses, kinds)
	}

	t.Setenv("GITHUB_TOKEN", "")
	offline, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if !offline.Passed || len(offline.Deprecations) != 0 {
		t.Fatalf("offline audit failed on unverified pins: %+v", offline.Deprecations)
	}
	for _, action := range offline.Actions {
		if action.Pin != PinUnverified || action.UpToDate {
			t.Errorf("offline %s: want unverified, got %+v", action.Action, action)
		}
	}
	if inventory := FormatActionsInventory(offline.Actions); !strings.Contains(inventory, "4 SHA pin(s) unverified: "+errNoPinLookup.Error()) {
		t.Errorf("offline inventory does not say why:\n%s", inventory)
	}
}
