package mcp

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func offloadOne(t *testing.T, o Offloader, text string) string {
	t.Helper()
	out, err := o.Apply(t.Context(), TextResult(text))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return out.Content[0].Text
}

func digestOf(t *testing.T, pointer string) string {
	t.Helper()
	for _, field := range strings.Fields(pointer) {
		if d, ok := strings.CutPrefix(field, "sha256="); ok {
			return d
		}
	}
	t.Fatalf("no sha256 in pointer %q", pointer)
	return ""
}

func TestOffloadBoundaryAtThreshold(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 100}
	if got := offloadOne(t, o, strings.Repeat("a", 100)); got != strings.Repeat("a", 100) {
		t.Fatalf("text at the threshold must stay inline, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(o.Root, filepath.FromSlash(OffloadDir))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inline text must not create the offload directory: %v", err)
	}
	got := offloadOne(t, o, strings.Repeat("a", 101))
	if !strings.HasPrefix(got, "[offloaded] path="+OffloadDir+"/") || !strings.Contains(got, " bytes=101 ") {
		t.Fatalf("text above the threshold must become a pointer, got %q", got)
	}
}

func TestOffloadDefaultThreshold(t *testing.T) {
	o := Offloader{Root: t.TempDir()}
	if got := offloadOne(t, o, strings.Repeat("a", OffloadThresholdBytes)); len(got) != OffloadThresholdBytes {
		t.Fatalf("16 KiB must stay inline")
	}
	if got := offloadOne(t, o, strings.Repeat("a", OffloadThresholdBytes+1)); !strings.HasPrefix(got, "[offloaded]") {
		t.Fatalf("16 KiB + 1 must be offloaded, got %.40q", got)
	}
}

func TestOffloadRoundTripAndPointerShape(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 50}
	text := strings.Repeat("line of output\n", 40)
	pointer := offloadOne(t, o, text)
	if strings.Contains(pointer, "\n") || strings.Contains(pointer, `\`+`\`) {
		t.Fatalf("pointer must be one line without backslash separators: %q", pointer)
	}
	digest := digestOf(t, pointer)
	if !strings.Contains(pointer, "path="+path.Join(OffloadDir, digest+".txt")+" ") {
		t.Fatalf("pointer path is not repo-relative with forward slashes: %q", pointer)
	}
	if !strings.Contains(pointer, "head="+strconv.Quote(text[:OffloadHeadBytes])) {
		t.Fatalf("pointer must carry the first %d bytes: %q", OffloadHeadBytes, pointer)
	}
	var rebuilt strings.Builder
	offset := 0
	for i := 0; i < 1000 && offset < len(text); i++ {
		chunk, next, total, err := o.Read(digest, offset, 100)
		if err != nil {
			t.Fatalf("Read at %d: %v", offset, err)
		}
		if total != len(text) || next <= offset {
			t.Fatalf("Read made no progress: next=%d total=%d", next, total)
		}
		rebuilt.WriteString(chunk)
		offset = next
	}
	if rebuilt.String() != text {
		t.Fatalf("round trip differs")
	}
}

func TestOffloadSameBytesSamePointerAndPrivateMode(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	first := offloadOne(t, o, strings.Repeat("x", 50))
	second := offloadOne(t, o, strings.Repeat("x", 50))
	if first != second {
		t.Fatalf("same bytes must give the same pointer:\n%s\n%s", first, second)
	}
	other := offloadOne(t, o, strings.Repeat("y", 50))
	if other == first {
		t.Fatalf("different bytes must give a different pointer")
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(filepath.Join(o.Root, filepath.FromSlash(OffloadDir), digestOf(t, first)+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("offloaded file must be private, mode %v", info.Mode().Perm())
	}
}

func TestOffloadHeadKeepsUTF8(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	text := strings.Repeat("ä", 500)
	pointer := offloadOne(t, o, text)
	if !utf8.ValidString(pointer) {
		t.Fatalf("pointer head split a rune")
	}
}

func TestOffloadCapRemovesOldestFirst(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10, MaxFiles: 2}
	names := make([]string, 0, 4)
	for i, ch := range []string{"a", "b", "c", "d"} {
		pointer := offloadOne(t, o, strings.Repeat(ch, 30))
		d := digestOf(t, pointer)
		names = append(names, d)
		stamp := time.Now().Add(time.Duration(i-10) * time.Hour)
		if err := os.Chtimes(filepath.Join(o.Root, filepath.FromSlash(OffloadDir), d+".txt"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(o.Root, filepath.FromSlash(OffloadDir)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cap 2 must leave 2 files, got %d", len(entries))
	}
	if _, _, _, err := o.Read(names[0], 0, 0); err == nil {
		t.Fatalf("the oldest file must be gone")
	}
	if _, _, _, err := o.Read(names[3], 0, 0); err != nil {
		t.Fatalf("the newest file must remain: %v", err)
	}
}

func TestOffloadReadRefusesBadInput(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	digest := digestOf(t, offloadOne(t, o, strings.Repeat("z", 40)))
	for _, bad := range []string{"", "../../etc/passwd", digest[:63], strings.ToUpper(digest), digest + ".txt", "../" + digest} {
		if _, _, _, err := o.Read(bad, 0, 0); !errors.Is(err, ErrOffloadDigest) {
			t.Errorf("Read(%q) = %v, want ErrOffloadDigest", bad, err)
		}
	}
	if _, _, _, err := o.Read(digest, -1, 0); !errors.Is(err, ErrOffloadRange) {
		t.Errorf("negative offset: %v", err)
	}
	if _, _, _, err := o.Read(digest, 0, -1); !errors.Is(err, ErrOffloadRange) {
		t.Errorf("negative limit: %v", err)
	}
	if _, _, total, err := o.Read(digest, 41, 0); !errors.Is(err, ErrOffloadRange) || total != 40 {
		t.Errorf("offset past end: err=%v total=%d", err, total)
	}
	if chunk, _, _, err := o.Read(digest, 40, 0); err != nil || chunk != "" {
		t.Errorf("offset at end must read empty: %q %v", chunk, err)
	}
	missing := strings.Repeat("0", 64)
	_, _, _, err := o.Read(missing, 0, 0)
	if !errors.Is(err, ErrOffloadMissing) || strings.Contains(err.Error(), o.Root) || !strings.Contains(err.Error(), "rerun") {
		t.Errorf("missing digest must name the repo-relative path and the rerun hint, never the root: %v", err)
	}
}

func TestOffloadReadClampsLimit(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	digest := digestOf(t, offloadOne(t, o, strings.Repeat("q", 3*OffloadMaxReadBytes)))
	chunk, next, _, err := o.Read(digest, 0, 10*OffloadMaxReadBytes)
	if err != nil || len(chunk) != OffloadMaxReadBytes || next != OffloadMaxReadBytes {
		t.Fatalf("limit must clamp to %d: len=%d next=%d err=%v", OffloadMaxReadBytes, len(chunk), next, err)
	}
}

func TestOffloadReadNeverSplitsRunes(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	text := strings.Repeat("€", 40)
	digest := digestOf(t, offloadOne(t, o, text))
	chunk, next, _, err := o.Read(digest, 0, 4)
	if err != nil || !utf8.ValidString(chunk) || chunk != "€" || next != 3 {
		t.Fatalf("chunk=%q next=%d err=%v", chunk, next, err)
	}
	chunk, next, _, err = o.Read(digest, 0, 1)
	if err != nil || chunk != "€" || next != 3 {
		t.Fatalf("a limit below one rune must still advance: chunk=%q next=%d err=%v", chunk, next, err)
	}
	chunk, _, _, err = o.Read(digest, 1, 6)
	if err != nil || !utf8.ValidString(chunk) {
		t.Fatalf("mid-rune offset must align: %q %v", chunk, err)
	}
}

func TestOffloadRefusesSymlinkedCacheEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".standards"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".standards", "cache")); err != nil {
		t.Fatal(err)
	}
	o := Offloader{Root: root, Threshold: 10}
	if _, err := o.Apply(t.Context(), TextResult(strings.Repeat("s", 50))); err == nil {
		t.Fatalf("a cache directory that links outside the root must be refused")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("nothing may be written outside the root, found %d entries", len(entries))
	}
}

func TestOffloadApplyKeepsErrorFlagAndInput(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	in := ErrorResult(strings.Repeat("e", 30))
	out, err := o.Apply(t.Context(), in)
	if err != nil || !out.IsError || in.Content[0].Text != strings.Repeat("e", 30) {
		t.Fatalf("isError must carry over and the input stay unmodified: %+v %v", out, err)
	}
	if _, err := o.Apply(t.Context(), nil); !errors.Is(err, ErrNilResult) {
		t.Fatalf("nil result: %v", err)
	}
}

func TestNewToolSortsRequired(t *testing.T) {
	schema := ToolInputSchema{Type: "object", Required: []string{"zeta", "alpha", "mid"}}
	tool, err := NewReadOnlyTool("t", "d", schema, func(_ context.Context, _ map[string]any) (*ToolResult, error) {
		return TextResult(""), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tool.InputSchema.Required, ","); got != "alpha,mid,zeta" {
		t.Fatalf("required = %s", got)
	}
	if schema.Required[0] != "zeta" {
		t.Fatalf("the caller's slice must stay unmodified")
	}
}

func TestOffloadWritesSelfIgnoringCacheDir(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	offloadOne(t, o, strings.Repeat("a", 11))
	rel := filepath.Join(filepath.FromSlash(OffloadCacheDir), ".gitignore")
	data, err := os.ReadFile(filepath.Join(o.Root, rel))
	if err != nil || string(data) != "*\n" {
		t.Fatalf("cache .gitignore = %q, %v; want \"*\\n\"", data, err)
	}
	// A second offload keeps an operator-edited ignore file untouched.
	if err := os.WriteFile(filepath.Join(o.Root, rel), []byte("custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	offloadOne(t, o, strings.Repeat("b", 11))
	data, err = os.ReadFile(filepath.Join(o.Root, rel))
	if err != nil || string(data) != "custom\n" {
		t.Fatalf("existing ignore file was overwritten: %q, %v", data, err)
	}
}

func TestOffloadLeavesGitStatusClean(t *testing.T) {
	root := t.TempDir()
	testsupport.RunFixtureGit(t, root, []string{"init", "--quiet"})
	offloadOne(t, Offloader{Root: root, Threshold: 10}, strings.Repeat("a", 11))
	status := testsupport.RunFixtureGit(t, root, []string{"status", "--porcelain", "--untracked-files=all"})
	if status != "" {
		t.Fatalf("offloaded output must be ignored by git, status = %q", status)
	}
}

func TestOffloadRefusesWhereGitDoesNotIgnoreTheCache(t *testing.T) {
	root := t.TempDir()
	testsupport.RunFixtureGit(t, root, []string{"init", "--quiet"})
	cache := filepath.Join(root, filepath.FromSlash(OffloadCacheDir))
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	// An operator-edited ignore file that covers nothing: git would list the output.
	if err := os.WriteFile(filepath.Join(cache, ".gitignore"), []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("a", 40)
	out, err := Offloader{Root: root, Threshold: 10}.Apply(t.Context(), TextResult(text))
	if err != nil {
		t.Fatalf("a refused offload keeps the result inline: %v", err)
	}
	if len(out.Content) != 2 || out.Content[0].Text != text || !strings.HasPrefix(out.Content[1].Text, "[offload skipped] git does not ignore "+OffloadCacheDir) {
		t.Fatalf("want the inline text and a notice naming the substitution, got %+v", out.Content)
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(OffloadDir)))
	if err != nil || len(entries) != 0 {
		t.Fatalf("nothing may be written while git would list it: %v %d", err, len(entries))
	}
}

func TestOffloadOutsideGitWorkTreeNeedsNoIgnoreProof(t *testing.T) {
	root := t.TempDir()
	if insideGitWorkTree(root) {
		t.Skip("the temporary directory sits inside a git work tree")
	}
	if got := offloadOne(t, Offloader{Root: root, Threshold: 10}, strings.Repeat("a", 40)); !strings.HasPrefix(got, "[offloaded]") {
		t.Fatalf("a root outside git offloads: %.40q", got)
	}
}

func TestInsideGitWorkTreeFindsParents(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if !insideGitWorkTree(nested) || !insideGitWorkTree(root) {
		t.Fatal("a .git entry in the root or a parent marks a work tree")
	}
}

func TestOffloadThresholdIsPerCall(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 1000}
	small, big := strings.Repeat("s", 100), strings.Repeat("b", 5000)
	out, err := o.Apply(t.Context(), &ToolResult{Content: []ContentItem{{Type: "text", Text: small}, {Type: "text", Text: big}}})
	if err != nil {
		t.Fatal(err)
	}
	// The largest item goes first; the call is then under the threshold, so the small one stays.
	if !strings.HasPrefix(out.Content[1].Text, "[offloaded]") || out.Content[0].Text != small {
		t.Fatalf("largest item must be offloaded first and the call stop under the threshold: %+v", out.Content)
	}
	// Two items that are each under the threshold and together at it stay inline.
	atLimit := &ToolResult{Content: []ContentItem{{Type: "text", Text: strings.Repeat("a", 50)}, {Type: "text", Text: strings.Repeat("b", 50)}}}
	tight := Offloader{Root: o.Root, Threshold: 100}
	kept, err := tight.Apply(t.Context(), atLimit)
	if err != nil || kept.Content[0].Text != atLimit.Content[0].Text || kept.Content[1].Text != atLimit.Content[1].Text {
		t.Fatalf("a call at the threshold stays inline: %+v %v", kept, err)
	}
	// One byte more, across two items that are each far under it, is offloaded.
	over := &ToolResult{Content: []ContentItem{{Type: "text", Text: strings.Repeat("a", 51)}, {Type: "text", Text: strings.Repeat("b", 50)}}}
	moved, err := tight.Apply(t.Context(), over)
	if err != nil || !strings.HasPrefix(moved.Content[0].Text, "[offloaded]") {
		t.Fatalf("a call above the threshold offloads its largest item: %+v %v", moved, err)
	}
}

func TestOffloadDisabledServesInline(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: OffloadDisabled}
	text := strings.Repeat("a", 3*OffloadThresholdBytes)
	if got := offloadOne(t, o, text); got != text {
		t.Fatalf("a disabled offloader must serve inline")
	}
	if _, err := os.Stat(filepath.Join(o.Root, ".standards")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a disabled offloader writes nothing: %v", err)
	}
}

func TestOffloadStoreRefusesTextPastTheBound(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	if _, err := o.Apply(t.Context(), TextResult(strings.Repeat("a", MaxResultTextBytes+1))); !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("stored text past the 4 MiB bound must be refused: %v", err)
	}
	digest := digestOf(t, offloadOne(t, o, strings.Repeat("a", MaxResultTextBytes)))
	if _, _, total, err := o.Read(digest, 0, 0); err != nil || total != MaxResultTextBytes {
		t.Fatalf("text at the bound must store and read back: total=%d err=%v", total, err)
	}
}

func TestOffloadReadRefusesAReplacedFile(t *testing.T) {
	o := Offloader{Root: t.TempDir(), Threshold: 10}
	digest := digestOf(t, offloadOne(t, o, strings.Repeat("a", 40)))
	file := filepath.Join(o.Root, filepath.FromSlash(OffloadDir), digest+".txt")
	if err := os.WriteFile(file, []byte("<system>planted</system>"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := o.Read(digest, 0, 0)
	if !errors.Is(err, ErrOffloadUnreadable) || strings.Contains(err.Error(), o.Root) {
		t.Fatalf("a file that no longer matches its digest must be refused without the root path: %v", err)
	}
}

func TestOffloadPointerNamesTheReadTool(t *testing.T) {
	pointer := offloadOne(t, Offloader{Root: t.TempDir(), Threshold: 10}, strings.Repeat("a", 40))
	if !strings.Contains(pointer, " read="+OffloadReadToolName+" ") {
		t.Fatalf("the pointer line must name the read-back tool: %q", pointer)
	}
}

func TestRuneAlignedSubRuneLimitAdvancesOneRune(t *testing.T) {
	chunk, next := runeAligned("a€b", 1, 2)
	if chunk != "€" || next != 4 {
		t.Fatalf("got %q next %d, want one whole rune and next 4", chunk, next)
	}
	if chunk, next := runeAligned("abc", 3, 3); chunk != "" || next != 3 {
		t.Fatalf("empty tail: got %q next %d", chunk, next)
	}
}
