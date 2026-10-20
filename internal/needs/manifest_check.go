package needs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"go.yaml.in/yaml/v3"
)

// NeedsManifestName is the committed manifest `needs scan --write` produces.
const NeedsManifestName = ".needs.yaml"

// maxManifestDriftLines bounds the lines one drift report names (HISS-02); a longer drift
// ends with a count of the lines left out.
const maxManifestDriftLines = 40

// maxManifestLines bounds the lines one manifest comparison walks (HISS-02).
const maxManifestLines = 100000

// ErrNeedsManifestMissing reports that a repository has no committed .needs.yaml to check.
var ErrNeedsManifestMissing = errors.New("needs: no committed .needs.yaml; run 'praetorctl needs scan --write'")

// marshalNeedsManifest is the one serialization of a manifest: WriteNeedsManifest writes
// these bytes and ManifestDrift compares against them, so the check cannot drift from the
// writer.
func marshalNeedsManifest(repoNeeds *RepoNeeds) ([]byte, error) {
	data, err := yaml.Marshal(repoNeeds)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal needs manifest: %w", err)
	}
	return data, nil
}

// frameworkRemedy is the actionable guidance when a scan would drop a committed framework
// on a host with no framework configured.
const frameworkRemedy = "set framework.targets.<language> in the workstation or fleet settings (--workstation-config, PRAETOR_WORKSTATION_CONFIG or the install manifest), or pass --framework"

// ReadCommittedManifest reads the committed .needs.yaml under repoPath. It returns
// ErrNeedsManifestMissing when the file does not exist.
func ReadCommittedManifest(ctx context.Context, repoPath string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("needs: reading the committed manifest requires a context")
	}
	committed, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, NeedsManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNeedsManifestMissing
	}
	if err != nil {
		return nil, fmt.Errorf("needs: read %s: %w", NeedsManifestName, err)
	}
	return committed, nil
}

// parseCommittedManifest normalizes CRLF and parses the committed manifest bytes.
func parseCommittedManifest(committed []byte) (*RepoNeeds, error) {
	committed = bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))
	var recorded RepoNeeds
	if err := yaml.Unmarshal(committed, &recorded); err != nil {
		return nil, fmt.Errorf("needs: parse committed %s: %w", NeedsManifestName, err)
	}
	return &recorded, nil
}

// DroppedFramework reports an error wrapping ErrFrameworkNotConfigured when the committed
// manifest records a framework or builder kits and fresh has an empty Framework, so a write
// or check on a host without framework settings does not silently discard them.
func DroppedFramework(committed []byte, fresh *RepoNeeds) error {
	if fresh == nil {
		return errors.New("needs: cannot check dropped framework against a nil manifest")
	}
	recorded, err := parseCommittedManifest(committed)
	if err != nil {
		return err
	}
	if fresh.Framework != "" {
		return nil
	}
	if recorded.Framework == "" && len(recorded.BuilderKits) == 0 {
		return nil
	}
	var details []string
	if recorded.Framework != "" {
		details = append(details, fmt.Sprintf("framework %q", recorded.Framework))
	}
	if len(recorded.BuilderKits) > 0 {
		details = append(details, fmt.Sprintf("builder kits [%s]", strings.Join(recorded.BuilderKits, ", ")))
	}
	return fmt.Errorf("needs: committed manifest records %s; fresh scan has no target framework configured; %s: %w",
		strings.Join(details, " and "), frameworkRemedy, ErrFrameworkNotConfigured)
}

// CheckNeedsManifest compares the committed .needs.yaml under repoPath with the manifest a
// fresh scan would write. It returns "" when they match, a drift report otherwise, and
// ErrNeedsManifestMissing when no manifest is committed. A declared non-goal the fresh scan
// finds the repository still using fails first with ErrNonGoalContradicted, naming the
// non-goal and the packages (checkDeclaredNonGoals), so neither `needs scan --check` nor
// `--write` accepts a false declaration.
func CheckNeedsManifest(ctx context.Context, repoPath string, fresh *RepoNeeds) (string, error) {
	if ctx == nil {
		return "", errors.New("needs: checking the manifest requires a context")
	}
	if err := checkDeclaredNonGoals(fresh); err != nil {
		return "", err
	}
	committed, err := ReadCommittedManifest(ctx, repoPath)
	if err != nil {
		return "", err
	}
	return ManifestDrift(committed, fresh)
}

// ManifestDrift compares committed manifest bytes with the manifest fresh would serialize
// to, ignoring only updated_at: a scan stamps the time it ran, so a regenerated manifest
// always differs there and never anywhere else unless the scan found something new or the
// generator's schema moved on. CRLF line endings, as a Windows checkout may produce, are
// read as LF. It returns "" when the two match and a line report otherwise.
func ManifestDrift(committed []byte, fresh *RepoNeeds) (string, error) {
	if fresh == nil {
		return "", errors.New("needs: cannot compare against a nil manifest")
	}
	recorded, err := parseCommittedManifest(committed)
	if err != nil {
		return "", err
	}
	committed = bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))
	candidate := *fresh
	candidate.UpdatedAt = recorded.UpdatedAt
	want, err := marshalNeedsManifest(&candidate)
	if err != nil {
		return "", err
	}
	if bytes.Equal(committed, want) {
		return "", nil
	}
	return manifestLineDrift(string(committed), string(want))
}

// manifestLineDrift lists the committed lines a fresh scan no longer writes ("-") and the
// lines it writes that are not committed ("+"), each with its line number.
func manifestLineDrift(committed, generated string) (string, error) {
	oldLines := strings.Split(committed, "\n")
	newLines := strings.Split(generated, "\n")
	if len(oldLines) > maxManifestLines || len(newLines) > maxManifestLines {
		return "", fmt.Errorf("needs: manifest exceeds %d lines", maxManifestLines)
	}
	var report []string
	report = append(report, linesMissingFrom("- committed", oldLines, newLines)...)
	report = append(report, linesMissingFrom("+ generated", newLines, oldLines)...)
	if len(report) == 0 {
		// Same lines in another order: the report still has to say something.
		report = append(report, "  line order differs from the generator's")
	}
	if len(report) > maxManifestDriftLines {
		omitted := len(report) - maxManifestDriftLines
		report = append(report[:maxManifestDriftLines], fmt.Sprintf("  ... %d more line(s)", omitted))
	}
	return strings.Join(report, "\n"), nil
}

// linesMissingFrom reports each line of from, labelled and numbered, that other does not
// hold as often, so a repeated line removed once is still reported once.
func linesMissingFrom(label string, from, other []string) []string {
	available := make(map[string]int, len(other))
	for i := 0; i < len(other) && i < maxManifestLines; i++ {
		available[other[i]]++
	}
	var missing []string
	for i := 0; i < len(from) && i < maxManifestLines; i++ {
		if available[from[i]] > 0 {
			available[from[i]]--
			continue
		}
		missing = append(missing, fmt.Sprintf("%s:%d: %s", label, i+1, from[i]))
	}
	return missing
}
