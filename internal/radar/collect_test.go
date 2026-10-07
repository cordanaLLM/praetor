// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// acceptanceDir holds the #818 acceptance fixture: an activity feed with a new repository and a
// push, a paper feed and a releases listing, each with one entry before the window.
const acceptanceDir = "testdata/acceptance"

// collectAcceptance collects the acceptance fixture over the seven days before now.
func collectAcceptance(t *testing.T, now time.Time) (Digest, error) {
	t.Helper()
	registry, err := LoadRegistry(acceptanceDir, "radar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	window, err := NewWindow(now, 7)
	if err != nil {
		t.Fatal(err)
	}
	return Collect(context.Background(), registry, FixtureReader{Dir: acceptanceDir}, window)
}

// Positive: the acceptance fixture lists the new repository, the push, the paper and the release,
// each once, and nothing from before the window; the release published one second before now is
// in, the push one second before since is out.
func TestCollect_Positive_AcceptanceFixture(t *testing.T) {
	digest, err := collectAcceptance(t, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if digest.Read != 3 || len(digest.Failed) != 0 || len(digest.Sections) != 3 {
		t.Fatalf("digest = %+v", digest)
	}
	out := string(Render(digest))
	for _, want := range []string{
		"- 2026-10-05 [example-org created repository example-org/new-parser](https://github.com/example-org/new-parser)",
		"- 2026-10-03 [example-org pushed to main in example-org/tokenizer](https://github.com/example-org/tokenizer/compare/1111111...2222222)",
		"- 2026-10-02 [Linear-time byte-pair merging for tokenizers](https://example.org/papers/abs/2610.00001)",
		"- 2026-10-06 [v2.0.0](https://github.com/example-org/example-project/releases/tag/v2.0.0)",
	} {
		if strings.Count(out, want) != 1 {
			t.Errorf("digest lists %q %d times:\n%s", want, strings.Count(out, want), out)
		}
	}
	for _, old := range []string{"last week", "older survey", "v1.9.0"} {
		if strings.Contains(out, old) {
			t.Errorf("digest lists %q from before the window:\n%s", old, out)
		}
	}
}

// Negative: no reader, an invalid registry and a cancelled context fail with no digest; a
// FixtureReader refuses a kind it cannot read.
func TestCollect_Negative_InvalidInput(t *testing.T) {
	registry, err := LoadRegistry(acceptanceDir, "radar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	window, err := NewWindow(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(context.Background(), registry, nil, window); err == nil {
		t.Error("a nil reader collected")
	}
	if _, err := Collect(context.Background(), &Registry{Version: 1}, FixtureReader{Dir: acceptanceDir}, window); !errors.Is(err, ErrNoSources) {
		t.Errorf("empty registry: %v, want ErrNoSources", err)
	}
	if _, err := Collect(context.Background(), nil, FixtureReader{Dir: acceptanceDir}, window); err == nil {
		t.Error("a nil registry collected")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if digest, err := Collect(cancelled, registry, FixtureReader{Dir: acceptanceDir}, window); !errors.Is(err, context.Canceled) || digest.Read != 0 {
		t.Errorf("cancelled collect = %+v, %v", digest, err)
	}
	if _, err := (FixtureReader{Dir: acceptanceDir}).Read(context.Background(), Source{ID: "x", Kind: "manual"}); !errors.Is(err, ErrUnsupportedKind) {
		t.Errorf("manual kind: %v, want ErrUnsupportedKind", err)
	}
}

// copyFixture copies the acceptance fixture into a temporary directory, without the files named
// in skip.
func copyFixture(t *testing.T, skip ...string) string {
	t.Helper()
	dir := t.TempDir()
	names, err := os.ReadDir(acceptanceDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range names {
		if strings.Contains(strings.Join(skip, "\n")+"\n", entry.Name()+"\n") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(acceptanceDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Boundary: a later window over the unchanged fixture renders an empty digest; one failed source
// is named in the digest and the collection succeeds; when every source fails, the digest names
// each and the collection fails with ErrAllSourcesFailed.
func TestCollect_Boundary_UnchangedAndFailures(t *testing.T) {
	digest, err := collectAcceptance(t, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if err != nil || len(Render(digest)) != 0 {
		t.Fatalf("unchanged fixture: %q, %v; want an empty digest", Render(digest), err)
	}
	registry, err := LoadRegistry(acceptanceDir, "radar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	window, err := NewWindow(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), 7)
	if err != nil {
		t.Fatal(err)
	}
	oneMissing := copyFixture(t, "example-papers.xml")
	digest, err = Collect(context.Background(), registry, FixtureReader{Dir: oneMissing}, window)
	if err != nil || digest.Read != 2 || len(digest.Failed) != 1 || digest.Failed[0].Source.ID != "example-papers" {
		t.Fatalf("one failure = %+v, %v", digest, err)
	}
	if out := string(Render(digest)); !strings.Contains(out, "## Failed sources\n\n- example-papers: read fixture example-papers.xml") {
		t.Errorf("digest does not name the failed source:\n%s", out)
	}
	allMissing := t.TempDir()
	digest, err = Collect(context.Background(), registry, FixtureReader{Dir: allMissing}, window)
	if !errors.Is(err, ErrAllSourcesFailed) || len(digest.Failed) != 3 || digest.Read != 0 {
		t.Fatalf("all failed = %+v, %v; want ErrAllSourcesFailed", digest, err)
	}
	if out := string(Render(digest)); strings.Count(out, ": read fixture ") != 3 {
		t.Errorf("digest does not name every failed source:\n%s", out)
	}
}
