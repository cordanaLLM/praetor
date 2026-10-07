// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
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
	if out := string(Render(digest)); !strings.Contains(out, "## Failed sources\n\n- example-papers: fixture example-papers.xml is missing\n") {
		t.Errorf("digest does not name the failed source:\n%s", out)
	}
	allMissing := t.TempDir()
	digest, err = Collect(context.Background(), registry, FixtureReader{Dir: allMissing}, window)
	if !errors.Is(err, ErrAllSourcesFailed) || len(digest.Failed) != 3 || digest.Read != 0 {
		t.Fatalf("all failed = %+v, %v; want ErrAllSourcesFailed", digest, err)
	}
	if out := string(Render(digest)); strings.Count(out, " is missing\n") != 3 {
		t.Errorf("digest does not name every failed source:\n%s", out)
	}
}

// feedRegistry is a registry of one feed source per id, each at https://example.org/<id>.xml.
func feedRegistry(ids ...string) *Registry {
	registry := &Registry{Version: 1}
	for _, id := range ids {
		registry.Sources = append(registry.Sources, Source{ID: id, Kind: KindFeed, URL: "https://example.org/" + id + ".xml", Why: "why"})
	}
	return registry
}

// collectWindow collects registry from dir over the seven days before now.
func collectWindow(t *testing.T, registry *Registry, dir string, now time.Time) (Digest, error) {
	t.Helper()
	window, err := NewWindow(now, 7)
	if err != nil {
		t.Fatal(err)
	}
	return Collect(context.Background(), registry, FixtureReader{Dir: dir}, window)
}

// Negative: a planted file that is too large, not a regular file or a link leaving the fixture
// directory fails its source with the file name and a fixed reason; neither the fixture directory
// nor a link target reaches the failure text or the digest, and the cause stays reachable.
func TestFixtureReader_Negative_PathFreeReasons(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.xml"), make([]byte, MaxFeedBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.xml"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"big":    "fixture big.xml exceeds the read limit",
		"folder": "fixture folder.xml is not a regular file",
		"absent": "fixture absent.xml is missing",
	}
	causes := map[string]error{"big": util.ErrFileTooLarge, "folder": util.ErrNotRegularFile, "absent": os.ErrNotExist}
	target := filepath.Join(outside, "secret.xml")
	if err := os.WriteFile(target, []byte("<rss/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Creating a symbolic link needs a privilege Windows grants only to some accounts; without
	// it the link case is skipped and the other three still run.
	if err := os.Symlink(target, filepath.Join(dir, "link.xml")); err == nil {
		want["link"] = "fixture link.xml resolves outside the fixture directory"
		causes["link"] = util.ErrPathEscapesRoot
	} else {
		t.Logf("link case skipped: %v", err)
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	digest, err := collectWindow(t, feedRegistry(ids...), dir, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrAllSourcesFailed) || len(digest.Failed) != len(want) {
		t.Fatalf("collect = %+v, %v", digest, err)
	}
	out := string(Render(digest))
	for _, failure := range digest.Failed {
		if failure.Reason != want[failure.Source.ID] || !strings.Contains(out, "- "+failure.Source.ID+": "+want[failure.Source.ID]+"\n") {
			t.Errorf("%s: reason %q, want %q in the digest:\n%s", failure.Source.ID, failure.Reason, want[failure.Source.ID], out)
		}
		_, readErr := FixtureReader{Dir: dir}.Read(context.Background(), failure.Source)
		if !errors.Is(readErr, causes[failure.Source.ID]) {
			t.Errorf("%s: %v does not wrap %v", failure.Source.ID, readErr, causes[failure.Source.ID])
		}
	}
	for _, local := range []string{dir, outside, "secret"} {
		if strings.Contains(out, Neutralize(local, MaxReasonRunes)) || strings.Contains(out, local) {
			t.Errorf("the digest carries the local name %q:\n%s", local, out)
		}
	}
}

// undatedFeed is a feed with count undated entries and, when dated is set, one entry dated
// 2 October 2026.
func undatedFeed(count int, dated bool) string {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "<item><title>undated %d</title><pubDate>Thu, 01 Oct 2026 09:30:00 CEST</pubDate></item>", i)
	}
	if dated {
		b.WriteString("<item><title>dated</title><pubDate>Fri, 02 Oct 2026 09:30:00 GMT</pubDate></item>")
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

// Boundary: undated entries are counted in a source's section only beside an entry inside the
// window, one in the singular; a window with nothing new over the same feed renders an empty
// digest and still counts them in Digest.Undated; a feed of undated entries alone never renders.
func TestCollect_Boundary_UndatedOnlyBesideNewItems(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("one.xml", undatedFeed(1, true))
	write("two.xml", undatedFeed(2, true))
	write("only.xml", undatedFeed(3, false))
	registry := feedRegistry("one", "two", "only")
	digest, err := collectWindow(t, registry, dir, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if err != nil || len(digest.Sections) != 2 || digest.Undated != 6 {
		t.Fatalf("window with new items = %+v, %v", digest, err)
	}
	out := string(Render(digest))
	for _, want := range []string{
		"## one (feed)", "1 entry of this source carries no readable date and is in no window.",
		"## two (feed)", "2 entries of this source carry no readable date and are in no window.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## only") || strings.Contains(out, "3 entries") {
		t.Errorf("a source with undated entries alone has a section:\n%s", out)
	}
	for _, later := range []time.Time{time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), time.Date(2027, 11, 7, 0, 0, 0, 0, time.UTC)} {
		digest, err = collectWindow(t, registry, dir, later)
		if err != nil || len(Render(digest)) != 0 || digest.Undated != 6 {
			t.Errorf("unchanged feed at %s: %q, undated %d, %v; want an empty digest", later, Render(digest), digest.Undated, err)
		}
	}
}
