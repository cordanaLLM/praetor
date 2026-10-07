package hisscoverage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// CatalogFile is the declared enforcement evidence, relative to the repository root.
	CatalogFile = ".config/hiss/coverage.yaml"
	// FixtureDir holds the corpus that backs every claim in the catalog. It is named testdata
	// because the HISS scanner already ignores that directory name, and these files are
	// inputs to the rules rather than source governed by them: scanning them would report
	// the engine's own test data as the repository's debt.
	FixtureDir = ".config/hiss/testdata"
	// maxCatalogBytes bounds the catalog read (HISS-02).
	maxCatalogBytes = 256 * 1024
	// maxFixturesPerBucket bounds one positive, negative, gap or measured directory (HISS-02).
	maxFixturesPerBucket = 256
)

// Bucket names within a fixture directory. A measured fixture holds a shape the scanner
// measures and reports without enforcing it: it must yield a measurement for the rule and no
// violation. A negative fixture must yield neither.
const (
	bucketPositive = "positive"
	bucketNegative = "negative"
	bucketGap      = "gap"
	bucketMeasured = "measured"
)

// ErrCatalogAbsent reports that no catalog is present.
var ErrCatalogAbsent = errors.New("hisscoverage: no coverage catalog")

// ErrClaimUnsupported reports a claim the corpus contradicts.
var ErrClaimUnsupported = errors.New("hisscoverage: declared coverage is not supported by its fixtures")

// LoadCatalog reads and validates the declared enforcement evidence.
func LoadCatalog(ctx context.Context, rootDir string) (*Catalog, error) {
	data, err := readCatalogFile(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	return parseCatalog(data)
}

// readCatalogFile reads the coverage file below rootDir, bounded by maxCatalogBytes.
func readCatalogFile(ctx context.Context, rootDir string) ([]byte, error) {
	path := filepath.Join(rootDir, filepath.FromSlash(CatalogFile))
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrCatalogAbsent, CatalogFile)
		}
		return nil, fmt.Errorf("read coverage catalog: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("coverage catalog exceeds %d bytes", maxCatalogBytes)
	}
	return data, nil
}

// parseCatalog decodes and validates the text of a coverage file.
func parseCatalog(data []byte) (*Catalog, error) {
	catalog, err := decodeCatalog(data)
	if err != nil {
		return nil, err
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return catalog, nil
}

// decodeCatalog parses exactly one document with no unknown fields, so a misspelled key is
// an error rather than a silently dropped claim.
func decodeCatalog(data []byte) (*Catalog, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: document is empty", ErrCatalogAbsent)
		}
		return nil, fmt.Errorf("decode coverage catalog: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: catalog must contain exactly one document", ErrInvalidCatalog)
	}
	return &catalog, nil
}

// Finding is one way the corpus contradicted the catalog.
type Finding struct {
	Rule     string
	Language string
	Bucket   string
	Fixture  string
	Detail   string
}

// String renders a finding for a terminal report.
func (f Finding) String() string {
	return fmt.Sprintf("%s/%s %s/%s: %s", f.Rule, f.Language, f.Bucket, f.Fixture, f.Detail)
}

// Report is the outcome of replaying the corpus against the catalog.
type Report struct {
	Claims   int
	Fixtures int
	Findings []Finding
	// Unbacked lists claims of detection that have no positive fixture at all. They are
	// reported separately because an unbacked claim is not yet wrong, it is merely
	// undemonstrated, and the distinction matters while the corpus is being filled in.
	Unbacked []string
	// Delegated lists claims decided by a tool this package does not run. Their enforcement
	// is not proven here, only their attribution, and saying so is the point: a claim this
	// gate cannot replay must not be indistinguishable from one it verified.
	Delegated []string
}

// Passed reports whether every claim survived its fixtures.
func (r *Report) Passed() bool {
	return r != nil && len(r.Findings) == 0
}

// Verify replays the fixture corpus and reports every claim the corpus contradicts.
//
// The check runs in both directions. A state claiming detection must report each of its
// positive fixtures, and a state claiming none must leave its gap fixtures undetected. The
// second direction is what stops the catalog from quietly becoming pessimistic: when a rule
// gains coverage, the gap fixture it was supposed to miss now fires and the catalog must be
// updated to say so.
func Verify(ctx context.Context, rootDir string, catalog *Catalog) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("hisscoverage: verify requires a context")
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	report := &Report{}
	for i := 0; i < len(catalog.Rules); i++ {
		rule := &catalog.Rules[i]
		for j := 0; j < len(rule.Coverage); j++ {
			if err := verifyClaim(ctx, rootDir, rule.ID, &rule.Coverage[j], report); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(report.Findings, func(a, b int) bool {
		return report.Findings[a].String() < report.Findings[b].String()
	})
	return report, nil
}

// verifyClaim replays one rule/language claim against its fixture buckets.
func verifyClaim(ctx context.Context, rootDir, ruleID string, cov *Coverage, report *Report) error {
	report.Claims++
	base := filepath.Join(rootDir, filepath.FromSlash(FixtureDir), ruleID, cov.Language)
	if err := verifyMeasured(ctx, base, ruleID, cov, report); err != nil {
		return err
	}
	if !cov.ReplayedHere() {
		return verifyDelegatedClaim(ctx, base, ruleID, cov, report)
	}

	positives, err := replayBucket(ctx, base, bucketPositive, ruleID, report)
	if err != nil {
		return err
	}
	if cov.State.ClaimsDetection() {
		if len(positives) == 0 {
			report.Unbacked = append(report.Unbacked,
				fmt.Sprintf("%s/%s claims %s with no positive fixture", ruleID, cov.Language, cov.State))
		}
		appendFindings(report, ruleID, cov.Language, bucketPositive, positives, undetected,
			"declared "+string(cov.State)+" but the fixture is not reported")
	} else {
		appendFindings(report, ruleID, cov.Language, bucketPositive, positives, detected,
			"declared "+string(cov.State)+" yet the fixture is reported; the catalog understates coverage")
	}

	negatives, err := replayBucket(ctx, base, bucketNegative, ruleID, report)
	if err != nil {
		return err
	}
	appendFindings(report, ruleID, cov.Language, bucketNegative, negatives, detected,
		"legitimate code is reported; the rule over-matches")
	appendFindings(report, ruleID, cov.Language, bucketNegative, negatives, measured,
		"legitimate code is measured over a limit; the measurement over-matches")

	gaps, err := replayBucket(ctx, base, bucketGap, ruleID, report)
	if err != nil {
		return err
	}
	appendFindings(report, ruleID, cov.Language, bucketGap, gaps, detected,
		"a fixture recorded as an undetected gap is now reported; close the gap in the catalog")
	return nil
}

// verifyMeasured replays the measured bucket in both directions: each fixture must yield a
// measurement for the rule, so losing the measurement fails, and none may yield a violation,
// so a measurement that starts to enforce fails too. Together with the negative bucket, whose
// fixtures sit exactly at the limits and must yield no measurement, this pins the limits
// from both sides.
func verifyMeasured(ctx context.Context, base, ruleID string, cov *Coverage, report *Report) error {
	results, err := replayBucket(ctx, base, bucketMeasured, ruleID, report)
	if err != nil {
		return err
	}
	appendFindings(report, ruleID, cov.Language, bucketMeasured, results, unmeasured,
		"declared measured but the fixture yields no measurement")
	appendFindings(report, ruleID, cov.Language, bucketMeasured, results, detected,
		"a measured fixture is reported as a violation; a measurement must never be enforced")
	return nil
}

// verifyDelegatedClaim checks a claim whose runner is not the HISS scanner.
//
// What can be verified here is the attribution, not the enforcement: the deciding tool is
// golangci-lint, gitleaks, the forge commit check or a CI step, none of which this package
// runs. So every bucket is held to one expectation -- the scanner must report none of them.
// If it reports one, the claim names the wrong mechanism, and a rule credited to a tool that
// is not actually deciding it is precisely the defect the catalog exists to surface.
//
// The delegation itself is recorded so a reader can see which claims this gate proves and
// which it only attributes. An unreplayed claim that passes silently would be indistinguishable
// from a verified one, which is the state this whole mechanism replaced.
func verifyDelegatedClaim(ctx context.Context, base, ruleID string, cov *Coverage, report *Report) error {
	report.Delegated = append(report.Delegated,
		fmt.Sprintf("%s/%s is decided by %s, not replayed here", ruleID, cov.Language, cov.Runner))
	for _, bucket := range []string{bucketPositive, bucketNegative, bucketGap} {
		results, err := replayBucket(ctx, base, bucket, ruleID, report)
		if err != nil {
			return err
		}
		appendFindings(report, ruleID, cov.Language, bucket, results, detected,
			"attributed to "+cov.Runner+" yet the HISS scanner reports it; the runner is wrong")
	}
	return nil
}

// fixtureResult records what the scanner said about one fixture for one rule.
type fixtureResult struct {
	name string
	// detected is true when the rule reported the fixture as a violation.
	detected bool
	// measured is true when the scanner measured the fixture over a limit of the rule.
	measured bool
}

// Predicates selecting the fixtures a finding is recorded for.
func detected(r fixtureResult) bool   { return r.detected }
func undetected(r fixtureResult) bool { return !r.detected }
func measured(r fixtureResult) bool   { return r.measured }
func unmeasured(r fixtureResult) bool { return !r.measured }

// appendFindings records a finding for every fixture the predicate selects.
func appendFindings(report *Report, ruleID, lang, bucket string, results []fixtureResult, selects func(fixtureResult) bool, detail string) {
	for i := 0; i < len(results); i++ {
		if selects(results[i]) {
			report.Findings = append(report.Findings, Finding{
				Rule: ruleID, Language: lang, Bucket: bucket, Fixture: results[i].name, Detail: detail,
			})
		}
	}
}

// replayBucket scans every fixture in one bucket and reports whether the rule fired on it.
// An absent bucket is not an error: a claim may legitimately have no negative or gap case.
// base is the claim's directory, <FixtureDir>/<rule>/<language>, so its name is the language
// the fixtures are staged as (hiss.FixturePath).
func replayBucket(ctx context.Context, base, bucket, ruleID string, report *Report) ([]fixtureResult, error) {
	dir := filepath.Join(base, bucket)
	staged := fixtureStage{ruleID: ruleID, language: filepath.Base(base)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if util.DirectoryAbsent(dir, err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read fixtures %s: %w", dir, err)
	}
	results := make([]fixtureResult, 0, len(entries))
	for i := 0; i < len(entries) && i < maxFixturesPerBucket; i++ {
		if entries[i].IsDir() {
			continue
		}
		result, err := staged.reported(ctx, dir, entries[i].Name())
		if err != nil {
			return nil, err
		}
		report.Fixtures++
		results = append(results, result)
	}
	return results, nil
}

// fixtureStage is the rule a bucket's fixtures are replayed for and the language they are
// staged as.
type fixtureStage struct {
	ruleID   string
	language string
}

// reported scans one fixture in isolation and reports whether the rule fired as a violation
// and whether the scanner measured it over one of the rule's limits.
//
// Each fixture is scanned alone in a temporary directory so a finding cannot be attributed
// to a neighbouring file, and so a fixture that fails to parse cannot silently suppress the
// rest of its bucket. It is staged where the scan reads its language (hiss.FixturePath): a
// GitHub Actions workflow in .github/workflows, any other fixture at the root.
func (stage fixtureStage) reported(ctx context.Context, dir, name string) (result fixtureResult, err error) {
	result.name = name
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(dir, name))
	if err != nil {
		return result, fmt.Errorf("read fixture %s: %w", name, err)
	}
	tmp, err := os.MkdirTemp("", "hiss-fixture-")
	if err != nil {
		return result, fmt.Errorf("create fixture root: %w", err)
	}
	// A cleanup failure leaves a scratch directory behind and is joined into the result
	// rather than discarded: a verifier that silently ignores its own errors is the shape
	// this package exists to detect.
	defer func() { err = errors.Join(err, os.RemoveAll(tmp)) }()
	target := filepath.Join(tmp, filepath.FromSlash(hiss.FixturePath(stage.language, name)))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return result, fmt.Errorf("stage fixture %s: %w", name, err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return result, fmt.Errorf("stage fixture %s: %w", name, err)
	}
	rep, scanErr := hiss.Scan(ctx, tmp, hiss.ScanOptions{})
	if scanErr != nil {
		return result, fmt.Errorf("scan fixture %s: %w", name, scanErr)
	}
	result.detected = rep.Breakdown[stage.ruleID] > 0
	result.measured = measuresRule(rep.Complexity.Measurements, stage.ruleID)
	return result, nil
}

// measuresRule reports whether any measurement belongs to ruleID.
func measuresRule(measurements []hiss.Measurement, ruleID string) bool {
	for i := 0; i < len(measurements); i++ {
		if measurements[i].RuleID == ruleID {
			return true
		}
	}
	return false
}
