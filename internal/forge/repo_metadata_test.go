package forge

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// metadataForge is a fake repository endpoint holding live metadata. GET returns it, PATCH
// merges the body into it, and PUT .../topics replaces the topics; failStatus, when set for a
// method, answers that method with the status and a body carrying control bytes.
type metadataForge struct {
	live       map[string]any
	failStatus map[string]int
	fake       *fakeForgeServer
}

// newMetadataForge starts the fake over live and returns a driver for acme/widgets.
func newMetadataForge(t *testing.T, live map[string]any, failStatus map[string]int) (*GitHubDriver, *fakeForgeServer, *metadataForge) {
	t.Helper()
	m := &metadataForge{live: live, failStatus: failStatus}
	gh, fake := newFakeForge(t, m.serve(t))
	m.fake = fake
	return gh, fake, m
}

func (m *metadataForge) serve(t *testing.T) func(w http.ResponseWriter, r *http.Request, index int) {
	return func(w http.ResponseWriter, r *http.Request, index int) {
		if status := m.failStatus[r.Method]; status != 0 {
			w.WriteHeader(status)
			if _, err := w.Write([]byte("rejected\x1b[31m" + strings.Repeat("x", 4096))); err != nil {
				t.Errorf("write rejection body: %v", err)
			}
			return
		}
		body := m.fake.requests[index].Body
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets":
			writeJSON(t, w, http.StatusOK, m.live)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widgets":
			for k, v := range body {
				m.live[k] = v
			}
			writeJSON(t, w, http.StatusOK, m.live)
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/widgets/topics":
			m.live["topics"] = body["names"]
			writeJSON(t, w, http.StatusOK, map[string]any{"names": body["names"]})
		default:
			writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		}
	}
}

func liveMetadata() map[string]any {
	return map[string]any{
		"description": "old description", "homepage": "", "visibility": "public",
		"topics": []any{"legacy", "governance"},
	}
}

func writes(fake *fakeForgeServer) []string {
	var out []string
	for _, r := range fake.requests {
		if r.Method != http.MethodGet {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

// Positive: drifted description, homepage and topics are written; the topic the repository
// already has and the one only the repository carries are both kept, and visibility drift is
// reported without being written.
func TestReconcileRepositoryMetadata_Positive_WritesDrift(t *testing.T) {
	gh, fake, forge := newMetadataForge(t, liveMetadata(), nil)
	declared := config.RepositoryMetadata{
		Description: "Governance engine", Homepage: "https://example.org", Visibility: "private",
		Topics: []string{"Governance", "mcp-server"},
	}
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if want := []string{"description", "homepage"}; !reflect.DeepEqual(report.Updated, want) {
		t.Errorf("Updated = %v, want %v", report.Updated, want)
	}
	if want := []string{"mcp-server"}; !reflect.DeepEqual(report.AddedTopics, want) {
		t.Errorf("AddedTopics = %v, want %v", report.AddedTopics, want)
	}
	if report.VisibilityDrift != "manifest declares private, GitHub reports public" {
		t.Errorf("VisibilityDrift = %q", report.VisibilityDrift)
	}
	if got := writes(fake); !reflect.DeepEqual(got, []string{"PATCH /repos/acme/widgets", "PUT /repos/acme/widgets/topics"}) {
		t.Fatalf("writes = %v", got)
	}
	patch := fake.requests[1].Body
	if _, sent := patch["visibility"]; sent || patch["description"] != "Governance engine" || patch["homepage"] != "https://example.org" {
		t.Errorf("patch body = %v; visibility must never be written", patch)
	}
	if topics := forge.live["topics"]; !reflect.DeepEqual(topics, []any{"legacy", "governance", "mcp-server"}) {
		t.Errorf("live topics = %v; existing topics must be kept and the missing one added", topics)
	}
}

// Negative: no credential writes nothing and sends nothing.
func TestReconcileRepositoryMetadata_Negative_NoTokenNoRequest(t *testing.T) {
	gh, fake, _ := newMetadataForge(t, liveMetadata(), nil)
	gh.Token = ""
	if _, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Description: "x"}); err == nil {
		t.Fatal("a driver without a token reconciled metadata")
	}
	if len(fake.requests) != 0 {
		t.Fatalf("a driver without a token sent %d requests", len(fake.requests))
	}
}

// Negative: a rejected read or write is an error whose body preview is bounded and stripped of
// control bytes, and nothing after the failure is written. A topic write rejected after the
// description was written names the written field, since sync drops the partial report.
func TestReconcileRepositoryMetadata_Negative_ErrorBodiesSanitized(t *testing.T) {
	for method, want := range map[string]struct {
		writes  int
		written bool
	}{http.MethodGet: {0, false}, http.MethodPatch: {1, false}, http.MethodPut: {2, true}} {
		gh, fake, _ := newMetadataForge(t, liveMetadata(), map[string]int{method: http.StatusForbidden})
		declared := config.RepositoryMetadata{Description: "new", Topics: []string{"added"}}
		_, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
		if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("%s rejection: err = %v", method, err)
		}
		if strings.ContainsRune(err.Error(), '\x1b') || len(err.Error()) > 1024 {
			t.Errorf("%s rejection: the error carries a raw or unbounded body (%d bytes)", method, len(err.Error()))
		}
		if got := writes(fake); len(got) != want.writes {
			t.Errorf("%s rejection: writes = %v", method, got)
		}
		if named := strings.Contains(err.Error(), "repository description already written"); named != want.written {
			t.Errorf("%s rejection: error names the written description = %v, want %v: %v", method, named, want.written, err)
		}
	}
}

// Negative: a topic GitHub would refuse, or more topics than it accepts, fails before any request.
func TestReconcileRepositoryMetadata_Negative_InvalidTopicsRefusedLocally(t *testing.T) {
	tooMany := make([]string, MaxRepositoryTopics+1)
	for i := range tooMany {
		tooMany[i] = "t" + strings.Repeat("a", i+1)
	}
	for name, topics := range map[string][]string{
		"space":      {"go lang"},
		"underscore": {"go_lang"},
		"leading -":  {"-go"},
		"empty":      {"  "},
		"too long":   {strings.Repeat("a", 51)},
		"too many":   tooMany,
	} {
		gh, fake, _ := newMetadataForge(t, liveMetadata(), nil)
		if _, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Topics: topics}); err == nil {
			t.Errorf("%s: invalid topics were accepted", name)
		}
		if len(fake.requests) != 0 {
			t.Errorf("%s: invalid topics reached the forge with %d requests", name, len(fake.requests))
		}
	}
}

// Boundary: an unset field never clears the forge's value. An empty manifest reads the
// repository and writes nothing, and a topic that differs only in case is already present.
func TestReconcileRepositoryMetadata_Boundary_UnsetFieldsNeverClear(t *testing.T) {
	for name, declared := range map[string]config.RepositoryMetadata{
		"empty manifest":     {},
		"blank values":       {Description: "  ", Homepage: "", Topics: []string{}},
		"matching, any case": {Description: "old description", Visibility: "PUBLIC", Topics: []string{"GOVERNANCE"}},
	} {
		gh, fake, forge := newMetadataForge(t, liveMetadata(), nil)
		report, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := writes(fake); len(got) != 0 || len(report.Updated) != 0 || len(report.AddedTopics) != 0 || report.VisibilityDrift != "" {
			t.Errorf("%s: writes %v, report %+v; nothing may change", name, got, report)
		}
		if !reflect.DeepEqual(forge.live, liveMetadata()) {
			t.Errorf("%s: live metadata changed to %v", name, forge.live)
		}
	}
}

// Boundary: topics that would take the repository past GitHub's limit are refused right after
// the read, before the drifted description is patched, so the "nothing was written" the error
// states holds; a union of exactly the limit is written; and a write GitHub does not echo back
// is an error.
func TestReconcileRepositoryMetadata_Boundary_TopicLimitAndReadback(t *testing.T) {
	full := make([]any, MaxRepositoryTopics)
	for i := range full {
		full[i] = "live-" + strings.Repeat("a", i+1)
	}
	gh, fake, forge := newMetadataForge(t, map[string]any{"description": "old", "topics": full}, nil)
	declared := config.RepositoryMetadata{Description: "new", Topics: []string{"one-more"}}
	_, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
	if err == nil || !strings.Contains(err.Error(), "limit of 20") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("a topic past the limit: err = %v", err)
	}
	if got := writes(fake); len(got) != 0 || forge.live["description"] != "old" {
		t.Errorf("a run refused for its topics still wrote %v (description now %v)", got, forge.live["description"])
	}

	gh, fake, _ = newMetadataForge(t, map[string]any{"description": "old", "topics": full[1:]}, nil)
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
	if err != nil || !reflect.DeepEqual(report.AddedTopics, []string{"one-more"}) || len(writes(fake)) != 2 {
		t.Fatalf("a union of exactly %d topics: report %+v, writes %v, err %v", MaxRepositoryTopics, report, writes(fake), err)
	}

	gh, _ = newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, map[string]any{"description": "unchanged"})
	})
	_, err = gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Description: "new"})
	if err == nil || !strings.Contains(err.Error(), "readback") {
		t.Fatalf("a write GitHub did not echo back must fail, got %v", err)
	}
}

// Boundary: a declared homepage equal to the one GitHub carries produces no PATCH, and one that
// differs only by its trailing slash is written as declared.
func TestReconcileRepositoryMetadata_Boundary_MatchingHomepageNotPatched(t *testing.T) {
	live := func() map[string]any {
		return map[string]any{"description": "d", "homepage": "https://widgets.example/docs/", "topics": []any{}}
	}
	gh, fake, _ := newMetadataForge(t, live(), nil)
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Description: "d", Homepage: " https://widgets.example/docs/ "})
	if err != nil || len(report.Updated) != 0 || len(writes(fake)) != 0 {
		t.Fatalf("a matching homepage: report %+v, writes %v, err %v", report, writes(fake), err)
	}

	gh, fake, forge := newMetadataForge(t, live(), nil)
	report, err = gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Homepage: "https://widgets.example/docs"})
	if err != nil || !reflect.DeepEqual(report.Updated, []string{"homepage"}) || forge.live["homepage"] != "https://widgets.example/docs" {
		t.Fatalf("a homepage differing by its slash: report %+v, writes %v, err %v", report, writes(fake), err)
	}
}

// Regression: this repository's own manifest declares the homepage its published documentation
// site lives at (mkdocs.yml site_url), which is what GitHub carries, so its own sync --remote
// sends no PATCH. The manifest used to declare a host that does not resolve, and the first
// remote sync would have replaced the working Pages URL with it.
func TestReconcileRepositoryMetadata_Regression_RepositoryHomepageIsPublishedSite(t *testing.T) {
	root := filepath.Join("..", "..")
	m, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatalf("read this repository's manifest: %v", err)
	}
	site := mkdocsSiteURL(t, filepath.Join(root, "mkdocs.yml"))
	gh, fake, _ := newMetadataForge(t, map[string]any{
		"description": m.Repository.Description, "homepage": site, "visibility": m.Repository.Visibility,
		"topics": []any{},
	}, nil)
	if _, err := gh.ReconcileRepositoryMetadata(context.Background(), m.Repository); err != nil {
		t.Fatalf("reconcile the repository manifest: %v", err)
	}
	for _, write := range writes(fake) {
		if write == "PATCH /repos/acme/widgets" {
			t.Fatalf("repository.homepage %q differs from the published site %q, so sync --remote would overwrite it",
				m.Repository.Homepage, site)
		}
	}
}

// mkdocsSiteURL returns the site_url of the MkDocs configuration at path; a missing file or
// site_url fails the test rather than skipping it, so the regression check cannot pass unrun.
func mkdocsSiteURL(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the MkDocs configuration: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "site_url:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	t.Fatalf("%s declares no site_url", path)
	return ""
}

// Positive, negative and boundary: ValidateRepositoryTopics accepts what GitHub accepts,
// including a topic of exactly 50 characters and exactly 20 topics, and refuses one byte past
// either edge of each allowed character range.
func TestValidateRepositoryTopics(t *testing.T) {
	twenty := make([]string, MaxRepositoryTopics)
	for i := range twenty {
		twenty[i] = "t" + strings.Repeat("a", i+1)
	}
	for name, topics := range map[string][]string{
		"none":          nil,
		"edges":         {"a", "z", "0", "9", "a-z-0-9", "Upper-Case"},
		"50 characters": {strings.Repeat("a", maxTopicLength)},
		"20 topics":     twenty,
	} {
		if err := ValidateRepositoryTopics(topics); err != nil {
			t.Errorf("%s: a topic list GitHub accepts was refused: %v", name, err)
		}
	}
	for _, topic := range []string{"a`", "a{", "a/", "a:", "a.b", "a\x00"} {
		if err := ValidateRepositoryTopics([]string{topic}); err == nil {
			t.Errorf("topic %q holds a byte just outside the allowed ranges and was accepted", topic)
		}
	}
	if err := ValidateRepositoryTopics(append(twenty, "one-more")); err == nil {
		t.Error("21 topics were accepted")
	}
}
