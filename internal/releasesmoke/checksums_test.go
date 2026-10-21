// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package releasesmoke

import (
	"strings"
	"testing"
)

func TestChecksumParserRefusesMalformedInput(t *testing.T) {
	cases := []string{"", "\n", "bad  app\n", strings.Repeat("z", 64) + "  app\n", sums("app", "ok") + sums("app", "ok"), sums("../app", "ok"), strings.Repeat("a", 4097)}
	for i := 0; i < len(cases); i++ {
		p := newChecksumParser()
		_, err := p.Write([]byte(cases[i]))
		if err == nil {
			err = p.finish()
		}
		if err == nil {
			t.Fatalf("accepted malformed checksum case %d", i)
		}
	}
}

func TestChecksumParserChunkedAndBoundary(t *testing.T) {
	p := newChecksumParser()
	line := strings.TrimSuffix(sums("a file", "ok"), "\n")
	for i := 0; i < len(line); i++ {
		if _, err := p.Write([]byte{line[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.finish(); err != nil || len(p.entries) != 1 {
		t.Fatalf("entries=%v, err=%v", p.entries, err)
	}
	if p.entries[0].name != "a file" {
		t.Fatal(p.entries)
	}
	long := newChecksumParser()
	if _, err := long.Write([]byte(sums(strings.Repeat("a", 4030), "ok"))); err != nil {
		t.Fatal(err)
	}
	if err := long.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestAllChecksumFailuresAreNamed(t *testing.T) {
	manifest := sums("first", "ok") + sums("second", "ok") + sums("absent", "ok")
	assets := []fixtureAsset{asset("SHA256SUMS", manifest, 1), asset("first", "no", 2), asset("second", "no", 3), asset("extra", "ok", 4)}
	_, err := Run(t.Context(), fixtureDriver(t, assets, 200), Options{Tag: "v1", MaxBytes: 1024})
	if err == nil {
		t.Fatal("defective release passed")
	}
	for _, name := range []string{"first: SHA-256 mismatch", "second: SHA-256 mismatch", "absent: listed", "extra: missing"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s missing from %v", name, err)
		}
	}
}
