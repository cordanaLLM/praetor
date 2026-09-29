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
