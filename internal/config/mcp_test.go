// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
)

// TestMCPPolicy_Positive: a declared threshold is read as written, 0 is a declared opt-out
// that differs from an absent key, the section survives RenderManifest, and DeclaredMCP
// returns it; an absent section and a nil manifest declare none.
func TestMCPPolicy_Positive(t *testing.T) {
	for name, tc := range map[string]struct {
		section string
		want    int
	}{"value": {"offload_threshold_bytes: 65536", 65536}, "opt-out": {"offload_threshold_bytes: 0", 0},
		"lower bound": {"offload_threshold_bytes: 1024", 1024}, "upper bound": {"offload_threshold_bytes: 4194304", 4194304},
	} {
		m, err := LoadManifest(writeManifest(t, "version: 1\nmcp:\n  "+tc.section+"\n"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := m.DeclaredMCP()
		if got == nil || got.OffloadThresholdBytes == nil || *got.OffloadThresholdBytes != tc.want {
			t.Fatalf("%s: declared mcp = %+v; want %d", name, got, tc.want)
		}
		again, err := LoadManifest(writeManifest(t, mustRender(t, m)))
		if err != nil || again.DeclaredMCP() == nil || again.DeclaredMCP().OffloadThresholdBytes == nil || *again.DeclaredMCP().OffloadThresholdBytes != tc.want {
			t.Fatalf("%s: rendered manifest reloads as %+v, %v", name, again, err)
		}
	}
	empty, err := LoadManifest(writeManifest(t, "version: 1\nmcp: {}\n"))
	if err != nil || empty.DeclaredMCP() == nil || empty.DeclaredMCP().OffloadThresholdBytes != nil {
		t.Fatalf("empty section = %+v, %v; want a section with no threshold", empty, err)
	}
	absent, err := LoadManifest(writeManifest(t, "version: 1\n"))
	if err != nil || absent.DeclaredMCP() != nil || strings.Contains(mustRender(t, absent), "mcp") {
		t.Fatalf("absent section = %+v, %v; want none, and none rendered", absent, err)
	}
	var none *Manifest
	if none.DeclaredMCP() != nil {
		t.Fatal("a nil manifest declares an mcp section")
	}
}

// TestMCPPolicy_Negative: an unknown or repeated key, a non-integer value and a value outside
// 0 and the bounds are refused naming the key, by DecodeManifest too.
func TestMCPPolicy_Negative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"unknown key":  {"offload_treshold_bytes: 2048", `unknown or duplicated mcp field "offload_treshold_bytes"`},
		"repeated key": {"offload_threshold_bytes: 2048\n  offload_threshold_bytes: 4096", "mcp must be a mapping of at most 1 known keys"},
		"string":       {`offload_threshold_bytes: "big"`, "mcp offload_threshold_bytes must be an integer"},
		"below bound":  {"offload_threshold_bytes: 1023", "mcp.offload_threshold_bytes must be an integer from 1024 to 4194304; got 1023"},
		"negative":     {"offload_threshold_bytes: -1", "mcp.offload_threshold_bytes must be an integer from 1024 to 4194304; got -1"},
		"above bound":  {"offload_threshold_bytes: 4194305", "mcp.offload_threshold_bytes must be an integer from 1024 to 4194304; got 4194305"},
	}
	for name, tc := range cases {
		data := "version: 1\nmcp:\n  " + tc.section + "\n"
		if _, err := LoadManifest(writeManifest(t, data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: LoadManifest = %v; want %q", name, err, tc.want)
		}
		if _, err := DecodeManifest([]byte(data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: DecodeManifest = %v; want %q", name, err, tc.want)
		}
	}
}
