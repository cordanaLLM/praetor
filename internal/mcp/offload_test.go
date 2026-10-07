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
)

func offloadOne(t *testing.T, o Offloader, text string) string {
	t.Helper()
	out, err := o.Apply(TextResult(text))
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
	if _, _, _, err := o.Read(missing, 0, 0); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing digest: %v", err)
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
	if _, err := o.Apply(TextResult(strings.Repeat("s", 50))); err == nil {
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
	out, err := o.Apply(in)
	if err != nil || !out.IsError || in.Content[0].Text != strings.Repeat("e", 30) {
		t.Fatalf("isError must carry over and the input stay unmodified: %+v %v", out, err)
	}
	if _, err := o.Apply(nil); !errors.Is(err, ErrNilResult) {
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
