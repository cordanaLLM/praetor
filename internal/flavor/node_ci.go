package flavor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// npmLockfile is the one lockfile the scaffolded Node CI job (templates/node/ci-node.yml.tmpl)
// can install from. setup-node's `cache: npm` fails the job when the repository root holds none
// of package-lock.json, npm-shrinkwrap.json and yarn.lock, and `npm ci` needs package-lock.json:
// npm 12, which Node 26 ships and `lts/*` selects once Node 26 is LTS, reads no
// npm-shrinkwrap.json, and a yarn.lock is not an npm lockfile at all.
const npmLockfile = "package-lock.json"

// nodeManifest is what the Node CI job reads from package.json.
type nodeManifest struct {
	PackageManager string
	Scripts        map[string]string
}

// npmCIRequirement reports what the repository lacks for the scaffolded Node CI job to pass as
// written, or "" when it lacks nothing.
//
// The job runs `npm ci` and `npm test`, and adoption makes it a required status check.
// typescript-node detects any package.json, so it also claims a pnpm, Yarn or Bun project and a
// Go or Rust repository carrying a package.json for its commit tooling. Scaffolding the job
// there handed each of them a required check no pull request could pass. Which manager installs
// is decided the way adoption's verification plan decides it (nodemanifest.NpmRuns), so the
// Makefile and the CI job never disagree.
func npmCIRequirement(repoPath string) string {
	var missing []string
	if !util.FileExists(filepath.Join(repoPath, npmLockfile)) {
		missing = append(missing, "no "+npmLockfile+" for `npm ci` and setup-node's npm cache")
	}
	manifest, err := readNodeManifest(repoPath)
	switch {
	case err != nil:
		missing = append(missing, err.Error())
	case !nodemanifest.NpmRuns(manifest.PackageManager):
		missing = append(missing, fmt.Sprintf("package.json names packageManager %q, and the job installs with npm", manifest.PackageManager))
	case !nodemanifest.ScriptRuns(manifest.Scripts["test"]):
		missing = append(missing, "package.json has no test script `npm test` can pass (it is missing, blank, or the placeholder npm init writes)")
	}
	return strings.Join(missing, "; ")
}

// readNodeManifest reads the repository's root package.json, bounded like a setting read.
//
// Keys are matched exactly, as npm matches them. Unmarshalling into tagged struct fields would
// match case-insensitively, so a "Scripts" object npm never reads would count as the test script.
func readNodeManifest(repoPath string) (nodeManifest, error) {
	var manifest nodeManifest
	data, err := util.ReadConfinedLimited(repoPath, "package.json", maxSettingBytes)
	if err != nil {
		return manifest, fmt.Errorf("package.json is unreadable: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return manifest, fmt.Errorf("package.json does not parse: %w", err)
	}
	if err := unmarshalPresent(fields["packageManager"], &manifest.PackageManager); err != nil {
		return manifest, fmt.Errorf("package.json packageManager is not a string: %w", err)
	}
	if err := unmarshalPresent(fields["scripts"], &manifest.Scripts); err != nil {
		return manifest, fmt.Errorf("package.json scripts is not an object of strings: %w", err)
	}
	return manifest, nil
}

// unmarshalPresent decodes raw into target when the key was present, and leaves target at its
// zero value when it was not.
func unmarshalPresent(raw json.RawMessage, target any) error {
	if raw == nil {
		return nil
	}
	return json.Unmarshal(raw, target)
}
