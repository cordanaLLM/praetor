// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package baseline

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClassBaseline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []string
		class Class
		want  string
	}{
		{"HISS", []string{"HISS-01", "G101", "README-99"}, HISSClass, ""},
		{"README", []string{"README-00", "README-01"}, READMEClass, ""},
		{"empty", nil, READMEClass, ""},
		{"wrong HISS", []string{"README-01"}, HISSClass, "README-01"},
		{"wrong README", []string{"G101"}, READMEClass, "G101"},
		{"empty foreign rule", []string{""}, READMEClass, `first foreign rule ""`},
		{"mixed", []string{"HISS-01", "README-01"}, HISSClass, "HISS=1 entries, README=1 entries"},
		{"mixed reversed", []string{"README-01", "HISS-01"}, "", "HISS-01"},
		{"invalid class", nil, "unknown", "unknown class"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkClassLoad(t, tc.rules, tc.class, tc.want)
		})
	}
}

func TestLoadClassBaselineAbsentAndCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	b, err := LoadClassBaseline(path, READMEClass)
	if err != nil || !b.Absent {
		t.Fatalf("missing=%+v %v", b, err)
	}
	if err := os.WriteFile(path, []byte("{"), FilePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClassBaseline(path, HISSClass); err == nil {
		t.Fatal("accepted malformed baseline")
	}
	if _, err := LoadClassBaseline(t.TempDir(), READMEClass); err == nil {
		t.Fatal("accepted directory baseline")
	}
}

func checkClassLoad(t *testing.T, rules []string, class Class, want string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	entries := make([]Infraction, 0, len(rules))
	for i := 0; i < len(rules); i++ {
		entries = append(entries, Infraction{RuleID: rules[i]})
	}
	if err := SaveBaseline(path, &Baseline{Version: 1, Infractions: entries}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadClassBaseline(path, class)
	if want != "" {
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), path) {
			t.Fatalf("refusal=%v; want %s", err, want)
		}
		return
	}
	if err != nil || b.Absent || b.Count() != len(entries) {
		t.Fatalf("loaded=%+v %v", b, err)
	}
}

func TestValidateClassNil(t *testing.T) {
	var b *Baseline
	if err := b.ValidateClass("nil.json", HISSClass); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("nil class validation=%v", err)
	}
}
