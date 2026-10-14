package forge

import (
	"net/http"
	"strings"
	"testing"
)

// collaborator renders one collaborator entry with the given permissions.
func collaborator(push, maintain, admin bool) map[string]any {
	return map[string]any{"permissions": map[string]bool{"push": push, "maintain": maintain, "admin": admin, "pull": true}}
}

// Positive: collaborators who can push, maintain or administer count; a read-only one does not;
// the listing asks for every affiliation.
func TestWriteCollaborators_Positive_CountsWriters(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, []any{collaborator(true, false, false), collaborator(false, true, false),
			collaborator(false, false, true), collaborator(false, false, false)})
	})
	got, err := gh.WriteCollaborators(t.Context())
	if err != nil || got != 3 {
		t.Fatalf("WriteCollaborators = %d, %v; want 3", got, err)
	}
	if req := fake.requests[0]; req.Path != "/repos/acme/widgets/collaborators" || !strings.Contains(req.Query, "affiliation=all") {
		t.Fatalf("request %s?%s", req.Path, req.Query)
	}
}

// Negative: a forge error and a body that is not a collaborator list are errors, never a count.
func TestWriteCollaborators_Negative_ForgeErrors(t *testing.T) {
	for name, handler := range map[string]func(w http.ResponseWriter, r *http.Request, i int){
		"forbidden": func(w http.ResponseWriter, _ *http.Request, _ int) {
			writeJSON(t, w, http.StatusForbidden, map[string]string{"message": "x"})
		},
		"not json": func(w http.ResponseWriter, _ *http.Request, _ int) {
			if _, err := w.Write([]byte("<html>")); err != nil {
				t.Error(err)
			}
		},
	} {
		gh, _ := newFakeForge(t, handler)
		if got, err := gh.WriteCollaborators(t.Context()); err == nil {
			t.Errorf("%s: WriteCollaborators = %d, nil; want an error", name, got)
		}
	}
}

// Boundary: an empty listing is zero writers, and a full first page is followed by the next.
func TestWriteCollaborators_Boundary_EmptyAndPaged(t *testing.T) {
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) { writeJSON(t, w, http.StatusOK, []any{}) })
	if got, err := gh.WriteCollaborators(t.Context()); err != nil || got != 0 {
		t.Fatalf("empty listing = %d, %v", got, err)
	}
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, index int) {
		page := []any{collaborator(true, false, false)}
		if index == 0 {
			page = make([]any, issuesPerPage)
			for i := range page {
				page[i] = collaborator(false, false, false)
			}
		}
		writeJSON(t, w, http.StatusOK, page)
	})
	if got, err := gh.WriteCollaborators(t.Context()); err != nil || got != 1 || len(fake.requests) != 2 {
		t.Fatalf("paged listing = %d, %v after %d requests; want 1 after 2", got, err, len(fake.requests))
	}
}
