// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"strings"
	"testing"
)

// networkFlag reports whether a runtime command line chooses a network for the container.
func networkFlag(argv []string) bool {
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--net" || argv[i] == "--network" || strings.HasPrefix(argv[i], "--net=") || strings.HasPrefix(argv[i], "--network=") {
			return true
		}
	}
	return false
}

// The race stage names the toolchain container's own network namespace as the isolation of its
// test binaries (gating.ContainerTestIsolation): a test server on the container's loopback is out
// of reach of workstation processes only while the container runs on the runtime's default network.
// Positive: no runtime's command line chooses a network. Negative, planted: a host network flag in
// either spelling is detected.
func TestImageRunArgs_KeepTheRuntimeDefaultNetwork(t *testing.T) {
	opts := RunOptions{Name: "c1", Mounts: []string{"/repo"}, Workdir: "/repo", UID: 1000, GID: 1000}
	for _, runtime := range []Runtime{{Name: "docker"}, {Name: "podman"}, {Name: "docker", Rootless: true}} {
		argv := Image{Runtime: runtime, ID: "sha256:" + strings.Repeat("a", 64)}.RunArgs(opts, "go", "test", "./...")
		if networkFlag(argv) {
			t.Fatalf("%+v: the container must keep its own network namespace: %q", runtime, argv)
		}
	}
	for _, planted := range [][]string{{"run", "--network", "host"}, {"run", "--net=host"}, {"run", "--network=host"}} {
		if !networkFlag(planted) {
			t.Fatalf("planted host network not detected: %q", planted)
		}
	}
}
