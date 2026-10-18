// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package figures

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func playerMapFS(t *testing.T, modTime time.Time) fstest.MapFS {
	t.Helper()
	m := make(fstest.MapFS, MaxPlayerAssets)
	for i := 0; i < len(playerEmbedNames) && i < MaxPlayerAssets; i++ {
		name := playerEmbedNames[i]
		data, err := ReadPlayer(name)
		if err != nil {
			t.Fatalf("ReadPlayer(%s): %v", name, err)
		}
		m[name] = &fstest.MapFile{
			Data:    data,
			Mode:    0o755,
			ModTime: modTime,
		}
	}
	return m
}

// Positive: PlayerArchive is deterministic across repeated calls and independent of source file
// modification times or permissions; PlayerArchiveSHA256 matches the archive bytes.
func TestPlayerArchiveDeterminism(t *testing.T) {
	first, err := PlayerArchive()
	if err != nil {
		t.Fatalf("first PlayerArchive: %v", err)
	}
	second, err := PlayerArchive()
	if err != nil {
		t.Fatalf("second PlayerArchive: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("PlayerArchive returned different bytes across calls")
	}
	fromNonZeroTime, err := playerArchiveFrom(playerMapFS(t, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("playerArchiveFrom with non-zero ModTime: %v", err)
	}
	if !bytes.Equal(first, fromNonZeroTime) {
		t.Fatal("playerArchiveFrom depended on source file ModTime or mode")
	}
	digest, err := PlayerArchiveSHA256()
	if err != nil {
		t.Fatalf("PlayerArchiveSHA256: %v", err)
	}
	sum := sha256.Sum256(first)
	if want := hex.EncodeToString(sum[:]); digest != want {
		t.Fatalf("PlayerArchiveSHA256 = %s, want %s", digest, want)
	}
}

// Negative: a changed byte in any of the three player files changes the archive digest, and a
// missing file errors.
func TestPlayerArchiveSensitivityAndMissingFile(t *testing.T) {
	baseDigest, err := PlayerArchiveSHA256()
	if err != nil {
		t.Fatalf("PlayerArchiveSHA256: %v", err)
	}
	for i := 0; i < len(playerEmbedNames) && i < MaxPlayerAssets; i++ {
		name := playerEmbedNames[i]
		fsys := playerMapFS(t, time.Unix(0, 0).UTC())
		mutated := slices.Clone(fsys[name].Data)
		mutated[0] ^= 0xff
		fsys[name].Data = mutated
		got, err := playerArchiveSHA256From(fsys)
		if err != nil {
			t.Fatalf("playerArchiveSHA256From(%s mutated): %v", name, err)
		}
		if got == baseDigest {
			t.Fatalf("changing a byte in %s left digest %s unchanged", name, got)
		}
	}
	incomplete := playerMapFS(t, time.Unix(0, 0).UTC())
	delete(incomplete, "player/player.js")
	if _, err := playerArchiveFrom(incomplete); err == nil {
		t.Fatal("playerArchiveFrom succeeded with a missing player file")
	}
}

// Boundary: the archive gzip header carries no name and zero ModTime, and the tar entries are
// exactly loader.js, player.js, THIRD-PARTY-LICENSES.txt in that order with mode 0644, uid/gid 0,
// empty uname/gname, and Unix 0 ModTime.
func TestPlayerArchiveEntryListExact(t *testing.T) {
	raw, err := PlayerArchive()
	if err != nil {
		t.Fatalf("PlayerArchive: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	if gr.Name != "" || !gr.ModTime.IsZero() {
		t.Fatalf("gzip header Name=%q ModTime=%v, want empty and zero", gr.Name, gr.ModTime)
	}
	tr := tar.NewReader(gr)
	wantNames := []string{"loader.js", "player.js", "THIRD-PARTY-LICENSES.txt"}
	var gotNames []string
	for i := 0; i < MaxPlayerAssets+1; i++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		gotNames = append(gotNames, hdr.Name)
		verifyPlayerTarHeader(t, hdr, playerEmbedNames[len(gotNames)-1], tr)
	}
	if err := gr.Close(); err != nil {
		t.Fatalf("gr.Close: %v", err)
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("tar entries = %v, want %v", gotNames, wantNames)
	}
}

func verifyPlayerTarHeader(t *testing.T, hdr *tar.Header, embedName string, r io.Reader) {
	t.Helper()
	if hdr.Mode != 0o644 || hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
		t.Fatalf("%s header = mode %o uid %d gid %d uname %q gname %q",
			hdr.Name, hdr.Mode, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
	}
	if !hdr.ModTime.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("%s ModTime = %v, want Unix 0", hdr.Name, hdr.ModTime)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read tar body %s: %v", hdr.Name, err)
	}
	want, err := ReadPlayer(embedName)
	if err != nil {
		t.Fatalf("ReadPlayer(%s): %v", embedName, err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("%s tar body differs from embedded bytes", hdr.Name)
	}
}

func TestRenderAndClassifyPlayerLock(t *testing.T) {
	lock, err := RenderPlayerLock("v1.4.0")
	if err != nil {
		t.Fatalf("RenderPlayerLock: %v", err)
	}
	if known, crlf := IsPraetorPlayerLock(lock); !known || crlf {
		t.Fatalf("IsPraetorPlayerLock(LF) = %v, %v", known, crlf)
	}
	crlfLock := []byte(strings.ReplaceAll(string(lock), "\n", "\r\n"))
	if known, crlf := IsPraetorPlayerLock(crlfLock); !known || !crlf {
		t.Fatalf("IsPraetorPlayerLock(CRLF) = %v, %v", known, crlf)
	}
	if known, _ := IsPraetorPlayerLock([]byte(`{"asset": "other.tar.gz"}`)); known {
		t.Fatal("IsPraetorPlayerLock accepted foreign JSON")
	}
}
