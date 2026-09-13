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

func verifyPublicBaseline(ctx context.Context, dir string, original *hiss.ScanReport) (string, error) {
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
	if err := comparePublicBaseline(ctx, recorded, original); err != nil {
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
	canonical, err := json.Marshal(recorded)
	if err != nil {
		return nil, fmt.Errorf("encode canonical adopted baseline: %w", err)
	}
	if err := validateRepairShape(data, canonical); err != nil {
		return nil, fmt.Errorf("adopted baseline shape: %w", err)
	}
	return recorded, nil
}

func comparePublicBaseline(ctx context.Context, recorded *baseline.Baseline, original *hiss.ScanReport) error {
	if recorded.Version != 1 || recorded.TotalInfractions != len(recorded.Infractions) || recorded.TotalInfractions != original.TotalInfractions {
		return errors.New("adopted baseline version or count differs from the independent original scan")
	}
	// Fingerprints may legitimately collide at the same rule/file/line. Compare
	// the full entry multiset, retaining multiplicity while ignoring list order.
	expected := make(map[baseline.Infraction]int, len(original.Violations))
	for _, infraction := range publicInfractions(original) {
		expected[infraction]++
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
