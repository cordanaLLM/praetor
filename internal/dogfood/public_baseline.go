package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func verifyPublicBaseline(ctx context.Context, dir string, original *hiss.ScanReport, imageRefs, secretFindings []baseline.Infraction) (string, error) {
	if err := validatePublicScan(original); err != nil {
		return "", err
	}
	data, err := readPublicBaseline(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("read adopted baseline: %w", err)
	}
	recorded, err := parsePublicBaseline(data)
	if err != nil {
		return "", err
	}
	if err := comparePublicBaseline(ctx, recorded, original, imageRefs, secretFindings); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// A missing baseline is an error, including for a zero-debt original tree.
func readPublicBaseline(ctx context.Context, dir string) (data []byte, err error) {
	if ctx == nil {
		return nil, errors.New("baseline read requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, hiss.DefaultScanTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err = readPublicFile(root, ".standards-baseline.json", maxRepairReportBytes)
	if err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

func parsePublicBaseline(data []byte) (*baseline.Baseline, error) {
	if err := validateRepairJSON(data); err != nil {
		return nil, fmt.Errorf("adopted baseline JSON: %w", err)
	}
	recorded, err := baseline.ParseBaseline(data)
	if err != nil {
		return nil, err
	}
	if err := recorded.ValidateClass(".standards-baseline.json", baseline.HISSClass); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(recorded)
	if err != nil {
		return nil, fmt.Errorf("encode canonical adopted baseline: %w", err)
	}
	if err := validateRepairShape(data, canonical); err != nil {
		return nil, fmt.Errorf("adopted baseline shape: %w", err)
	}
	return recorded, nil
}

func checkBaselineCounts(recorded, expected *baseline.Baseline) error {
	if recorded.Version != 1 || recorded.Count() != len(recorded.Infractions) {
		return errors.New("adopted baseline version or count differs from the independent original scan")
	}
	if recorded.Count() != expected.Count() || recorded.Count() != len(expected.Infractions) {
		return errors.New("adopted baseline version or count differs from the independent original scan")
	}
	return nil
}

func comparePublicBaseline(ctx context.Context, recorded *baseline.Baseline, original *hiss.ScanReport, imageRefs, secretFindings []baseline.Infraction) error {
	expectedBase := publicExpectedBaseline(original, imageRefs, secretFindings)
	if err := checkBaselineCounts(recorded, expectedBase); err != nil {
		return err
	}
	// Fingerprints may legitimately collide at the same rule/file/line. Compare
	// the full entry multiset, retaining multiplicity while ignoring list order.
	expected := make(map[baseline.Infraction]int, len(expectedBase.Infractions))
	for i := 0; i < len(expectedBase.Infractions) && i < hiss.MaxInfractionsCap; i++ {
		expected[expectedBase.Infractions[i]]++
	}
	for i := 0; i < len(recorded.Infractions) && i < hiss.MaxInfractionsCap; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		infraction := recorded.Infractions[i]
		if expected[infraction] == 0 {
			return errors.New("adopted baseline entry differs from the independent original scan")
		}
		expected[infraction]--
	}
	return ctx.Err()
}

// publicScanBaseline gives independent HISS scans the same class interpretation as adoption.
// The debt ratchet uses it: the post-adoption scan is HISS-only, so its base stays HISS-only.
func publicScanBaseline(scan *hiss.ScanReport) *baseline.Baseline {
	return &baseline.Baseline{Version: 1, TotalInfractions: scan.TotalInfractions, Infractions: publicInfractions(scan)}
}

// publicExpectedBaseline is the baseline adoption records for the original tree: the independent
// HISS infractions plus the unexcused HISS-11 image references and HISS-12 secret findings adoption
// baselines too (supplychain.UnexcusedImageRefInfractions and secretscan.UnexcusedSecretInfractions).
// Every debt count and baseline comparison uses it.
func publicExpectedBaseline(scan *hiss.ScanReport, imageRefs, secretFindings []baseline.Infraction) *baseline.Baseline {
	infractions := publicInfractions(scan)
	combined := make([]baseline.Infraction, 0, len(infractions)+len(imageRefs)+len(secretFindings))
	combined = append(combined, infractions...)
	combined = append(combined, imageRefs...)
	combined = append(combined, secretFindings...)
	return &baseline.Baseline{Version: 1, TotalInfractions: scan.TotalInfractions + len(imageRefs) + len(secretFindings), Infractions: combined}
}
