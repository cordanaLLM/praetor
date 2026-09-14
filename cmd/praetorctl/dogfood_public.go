package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

type publicDogfoodFlags struct {
	enabled           *bool
	source, artifacts *string
	attempts          *int
}

func addPublicDogfoodFlags(fs *flag.FlagSet) publicDogfoodFlags {
	return publicDogfoodFlags{
		enabled:   fs.Bool("public-loop", false, "Retain public clone plan/apply/verification and repeat-apply evidence"),
		source:    fs.String("source-root", "", "Validated Praetor source bundle for public adoption (default: --path)"),
		artifacts: fs.String("artifacts", "", "Required retained public-loop evidence directory"),
		attempts:  fs.Int("attempts", 2, "Maximum public-loop applications including stability recheck (2..3)"),
	}
}

func runPublicDogfood(ctx context.Context, flags publicDogfoodFlags, host string, repos []string, apply bool) error {
	source := *flags.source
	if source == "" {
		source = host
	}
	report, err := dogfood.RunPublicLoop(ctx, dogfood.PublicLoopOptions{
		SourceRoot: source, ArtifactDir: *flags.artifacts, Repositories: repos, Apply: apply, MaxAttempts: *flags.attempts,
	})
	if report != nil {
		data, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil {
			return fmt.Errorf("encode public dogfood result: %w", encodeErr)
		}
		fmt.Println(string(data))
	}
	return err
}

func incompatiblePublicFlags(targets string, popular, verify bool, report string) bool {
	return targets != "" || popular || verify || report != ""
}
