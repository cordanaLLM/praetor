package util

import "testing"

func TestSplitImageReference(t *testing.T) {
	digest := "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, ref, repository, tag, digest string
	}{
		// Positive: the pinned forms bootstrap images take.
		{"tag and digest", "mcr.microsoft.com/devcontainers/base:ubuntu26.04@" + digest, "mcr.microsoft.com/devcontainers/base", "ubuntu26.04", digest},
		{"digest only", "ghcr.io/example/dev@" + digest, "ghcr.io/example/dev", "", digest},
		{"tag only", "golang:1.27-alpine", "golang", "1.27-alpine", ""},
		// Negative: a registry port is part of the repository, never a tag.
		{"registry port", "example.com:5000/base", "example.com:5000/base", "", ""},
		{"registry port and tag", "example.com:5000/base:v1@" + digest, "example.com:5000/base", "v1", digest},
		// Boundary: empty input and a reference that is only a digest.
		{"empty", "", "", "", ""},
		{"bare digest", "@" + digest, "", "", digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository, tag, gotDigest := SplitImageReference(tc.ref)
			if repository != tc.repository || tag != tc.tag || gotDigest != tc.digest {
				t.Fatalf("SplitImageReference(%q) = %q, %q, %q; want %q, %q, %q", tc.ref, repository, tag, gotDigest, tc.repository, tc.tag, tc.digest)
			}
		})
	}
}

func TestParseDockerFrom(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       DockerFrom
		ok         bool
	}{
		// Positive: an image, flags before it, a stage name and a lower-case keyword.
		{"image", "FROM golang:1.27", DockerFrom{Image: "golang:1.27"}, true},
		{"platform flag and stage", "FROM --platform=$BUILDPLATFORM golang:1.27 AS Build", DockerFrom{Image: "golang:1.27", Stage: "build"}, true},
		{"lower-case keywords", "  from scratch as final", DockerFrom{Image: "scratch", Stage: "final"}, true},
		// Negative: another instruction, a comment and a parser directive name no FROM.
		{"run", "RUN go build ./...", DockerFrom{}, false},
		{"comment", "# FROM golang", DockerFrom{}, false},
		{"directive", "# syntax=docker/dockerfile:1", DockerFrom{}, false},
		// Boundary: FROM alone, flags only, and an AS without a name.
		{"bare FROM", "FROM", DockerFrom{}, false},
		{"flags only", "FROM --platform=linux/amd64", DockerFrom{}, true},
		{"dangling AS", "FROM golang AS", DockerFrom{Image: "golang"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseDockerFrom(tc.line)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ParseDockerFrom(%q) = %+v, %v; want %+v, %v", tc.line, got, ok, tc.want, tc.ok)
			}
		})
	}
}
