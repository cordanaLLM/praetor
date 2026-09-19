package devcontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// splitImagePin separates a reference into repository and sha256 digest. The
// implicit docker.io/library prefix is normalised so a Dockerfile writing
// "golang:1.27-alpine@sha256:..." matches a constant carrying the full name.
func splitImagePin(ref string) (string, string) {
	name := strings.TrimSpace(ref)
	digest := ""
	if repository, sum, found := strings.Cut(name, "@"); found {
		name, digest = repository, sum
	}
	slash := strings.LastIndex(name, "/")
	if tag := strings.Index(name[slash+1:], ":"); tag >= 0 {
		name = name[:slash+1+tag]
	}
	return strings.TrimPrefix(name, "docker.io/library/"), digest
}

// dockerfileImageRef returns the image reference of a FROM instruction, skipping
// flags such as --platform, or "" when the line is not a FROM instruction.
func dockerfileImageRef(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
		return ""
	}
	for i := 1; i < len(fields) && i < MaxLoopLimit; i++ {
		if !strings.HasPrefix(fields[i], "--") {
			return fields[i]
		}
	}
	return ""
}

// pinnedDigestDrift reports whether EVERY FROM instruction naming the pinned
// repository carries exactly that digest. Stopping at the first match would let
// a later build stage of the same repository drift unseen, which is the
// condition this check exists to stop.
func pinnedDigestDrift(dockerfile []byte, pin string) error {
	repository, digest := splitImagePin(pin)
	if digest == "" {
		return fmt.Errorf("pinned image %q carries no sha256 digest", pin)
	}
	lines := strings.Split(string(dockerfile), "\n")
	matched := 0
	for i := 0; i < len(lines) && i < MaxLoopLimit; i++ {
		ref := dockerfileImageRef(lines[i])
		if ref == "" {
			continue
		}
		name, sum := splitImagePin(ref)
		if name != repository {
			continue
		}
		matched++
		if sum == "" {
			return fmt.Errorf("FROM %s is not digest-pinned", ref)
		}
		if sum != digest {
			return fmt.Errorf("FROM %s pins %s; the recorded constant pins %s", ref, sum, digest)
		}
	}
	if matched == 0 {
		return fmt.Errorf("no FROM instruction references %s", repository)
	}
	return nil
}

// TestPinnedImagesMatchTheDockerfilesThatUseThem binds the image constants to
// the files that build with them: a constant nothing checks drifts silently.
func TestPinnedImagesMatchTheDockerfilesThatUseThem(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tc := range []struct {
		name, path, pin string
	}{
		{"development image builder", filepath.Join(root, "docker", "dev", "Dockerfile"), DefaultBuilderImage},
		{"release image builder", filepath.Join(root, "build", "package", "Dockerfile"), DefaultBuilderImage},
		{"recorded bootstrap builder", filepath.Join(root, ".devcontainer", "Dockerfile.praetor"), DefaultBuilderImage},
		{"recorded bootstrap base", filepath.Join(root, ".devcontainer", "Dockerfile.praetor"), DefaultBaseImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := pinnedDigestDrift(data, tc.pin); err != nil {
				t.Fatalf("%s: %v", tc.path, err)
			}
		})
	}
}

// TestPinnedDigestDriftNamesItsReason covers the drift checker itself, so a
// green pin test means agreement rather than a check that cannot fail.
func TestPinnedDigestDriftNamesItsReason(t *testing.T) {
	const digest = "sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125"
	const other = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	const pin = "docker.io/library/golang:1.27-alpine@" + digest

	// 1. Positive: flags, a stage alias and a preceding unrelated FROM are fine.
	matching := "# comment\nFROM gcr.io/distroless/static-debian12:nonroot\nFROM --platform=$BUILDPLATFORM golang:1.27-alpine@" + digest + " AS builder\n"
	if err := pinnedDigestDrift([]byte(matching), pin); err != nil {
		t.Fatalf("agreeing Dockerfile reported drift: %v", err)
	}
	// Two stages of the same repository agreeing on the digest stay clean, so
	// the multi-stage case below fails on drift rather than on stage count.
	agreeing := matching + "FROM golang:1.27-alpine@" + digest + " AS test\n"
	if err := pinnedDigestDrift([]byte(agreeing), pin); err != nil {
		t.Fatalf("second agreeing stage reported drift: %v", err)
	}

	// 2. Negative and 3. boundary: every failure names its reason.
	for _, tc := range []struct {
		name, dockerfile, pin, reason string
	}{
		{"differing digest", "FROM golang:1.27-alpine@" + other + "\n", pin, "the recorded constant pins"},
		{"later stage drifts", "FROM golang:1.27-alpine@" + digest + " AS builder\nFROM golang:1.27-alpine@" + other + " AS test\n", pin, "the recorded constant pins"},
		{"later stage unpinned", "FROM golang:1.27-alpine@" + digest + " AS builder\nFROM golang:1.27-alpine AS test\n", pin, "is not digest-pinned"},
		{"no FROM instruction", "# only a comment\nRUN true\n", pin, "no FROM instruction references golang"},
		{"unpinned FROM", "FROM golang:1.27-alpine\n", pin, "is not digest-pinned"},
		{"unrelated repository only", "FROM alpine:3.20@" + digest + "\n", pin, "no FROM instruction references golang"},
		{"pin without digest", "FROM golang:1.27-alpine@" + digest + "\n", "docker.io/library/golang:1.27-alpine", "carries no sha256 digest"},
		{"empty Dockerfile", "", pin, "no FROM instruction references golang"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := pinnedDigestDrift([]byte(tc.dockerfile), tc.pin)
			if err == nil {
				t.Fatal("drift went unreported")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error does not name its reason: %v", err)
			}
		})
	}
}
