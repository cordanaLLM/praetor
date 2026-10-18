// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"bytes"
	"go/build/constraint"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/tools/secrets"
)

func TestLockedAssetInventory_3D(t *testing.T) {
	names := secrets.Names()
	want := []string{secrets.GateFile, secrets.RulesGenFile, secrets.PlaceholderFile}
	if !slices.Equal(names, want) {
		t.Fatalf("asset names = %v, want %v", names, want)
	}
	for _, name := range want {
		if _, err := secrets.Read(name); err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
	}
	if _, err := secrets.Read("gate/unknown.go"); err == nil {
		t.Fatal("expected error on unknown asset, got nil")
	}
}

func TestGateBuildConstraints_3D(t *testing.T) {
	for _, file := range []string{secrets.GateFile, secrets.RulesGenFile} {
		source, err := secrets.Read(file)
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := bytes.Cut(source, []byte("\n"))
		expr, err := constraint.Parse(string(first))
		if err != nil {
			t.Fatalf("parse constraint in %s: %v", file, err)
		}
		if expr.Eval(func(string) bool { return false }) {
			t.Fatalf("%s constraint holds without tag", file)
		}
		if !expr.Eval(func(tag string) bool { return tag == secrets.BuildTag }) {
			t.Fatalf("%s constraint does not hold with tag %s", file, secrets.BuildTag)
		}
	}

	phSource, err := secrets.Read(secrets.PlaceholderFile)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := bytes.Cut(phSource, []byte("\n"))
	expr, err := constraint.Parse(string(first))
	if err != nil {
		t.Fatalf("parse placeholder constraint: %v", err)
	}
	if !expr.Eval(func(string) bool { return false }) {
		t.Fatal("placeholder constraint must hold without tag")
	}
	if expr.Eval(func(tag string) bool { return tag == secrets.BuildTag }) {
		t.Fatalf("placeholder constraint must not hold with tag %s", secrets.BuildTag)
	}
}
