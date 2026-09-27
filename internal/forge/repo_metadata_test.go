package forge

import (
	"context"
	"net/http"
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
// control bytes, and nothing after the failure is written.
func TestReconcileRepositoryMetadata_Negative_ErrorBodiesSanitized(t *testing.T) {
	for method, wantWrites := range map[string]int{http.MethodGet: 0, http.MethodPatch: 1} {
		gh, fake, _ := newMetadataForge(t, liveMetadata(), map[string]int{method: http.StatusForbidden})
		declared := config.RepositoryMetadata{Description: "new", Topics: []string{"added"}}
		_, err := gh.ReconcileRepositoryMetadata(context.Background(), declared)
		if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("%s rejection: err = %v", method, err)
		}
		if strings.ContainsRune(err.Error(), '\x1b') || len(err.Error()) > 1024 {
			t.Errorf("%s rejection: the error carries a raw or unbounded body (%d bytes)", method, len(err.Error()))
		}
		if got := writes(fake); len(got) != wantWrites {
			t.Errorf("%s rejection: writes = %v", method, got)
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

// Boundary: topics that would take the repository past GitHub's limit are refused before the
// topic write, and a write GitHub does not echo back is an error.
func TestReconcileRepositoryMetadata_Boundary_TopicLimitAndReadback(t *testing.T) {
	full := make([]any, MaxRepositoryTopics)
	for i := range full {
		full[i] = "live-" + strings.Repeat("a", i+1)
	}
	gh, fake, _ := newMetadataForge(t, map[string]any{"topics": full}, nil)
	_, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Topics: []string{"one-more"}})
	if err == nil || !strings.Contains(err.Error(), "limit of 20") {
		t.Fatalf("a topic past the limit: err = %v", err)
	}
	if got := writes(fake); len(got) != 0 {
		t.Errorf("a topic past the limit was written: %v", got)
	}

	gh, _ = newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, map[string]any{"description": "unchanged"})
	})
	_, err = gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{Description: "new"})
	if err == nil || !strings.Contains(err.Error(), "readback") {
		t.Fatalf("a write GitHub did not echo back must fail, got %v", err)
	}
}
