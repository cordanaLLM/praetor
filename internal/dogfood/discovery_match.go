package dogfood

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
)

func matchingDiscoveryPaths(tree publicTree, kind string, matches []string) []string {
	paths := make([]string, 0)
	for path, digest := range tree {
		if !isRegularDiscoveryDigest(digest) || !discoveryPathMatches(path, kind, matches) {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func discoveryPathMatches(path, kind string, matches []string) bool {
	if kind == discoveryScannerExtension {
		ext := strings.ToLower(filepath.Ext(path))
		for _, match := range matches {
			if ext == match {
				return true
			}
		}
		return false
	}
	return discoveryMatches(filepath.Base(path), matches)
}

func discoveryMatches(base string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(base, strings.TrimPrefix(pattern, "*")) {
			return true
		}
		if base == pattern {
			return true
		}
	}
	return false
}

func isRegularDiscoveryDigest(digest string) bool {
	return len(digest) == 69 && digest[4] == ':' && isHexDigest(digest[5:])
}

func isHexDigest(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == sha256.Size*2
}

func discoveryEvidence(tree publicTree, paths []string) []CapabilityEvidence {
	evidence := make([]CapabilityEvidence, 0, minDiscovery(len(paths), maxDiscoveryEvidence))
	for i := 0; i < len(paths) && i < maxDiscoveryEvidence; i++ {
		digest := tree[paths[i]]
		evidence = append(evidence, CapabilityEvidence{Path: paths[i], SHA256: digest[5:]})
	}
	return evidence
}

func minDiscovery(a, b int) int {
	if a < b {
		return a
	}
	return b
}
