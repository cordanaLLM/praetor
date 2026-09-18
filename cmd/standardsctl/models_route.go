package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
)

// modelRouteLimitations states what a route result does not claim.
const modelRouteLimitations = "Configured cost estimates and task/capability declarations only; no current-price, measured latency/quality, provider availability, concurrency reservation or dispatch claim"

type modelRouteFlags struct {
	task, capabilities, usage *string
	input, output             *int64
}

type modelRouteReport struct {
	*router.TaskRoute
	ConfigSHA256   string     `json:"config_sha256"`
	CapacitySource string     `json:"capacity_source"`
	CapturedAt     *time.Time `json:"capacity_captured_at,omitempty"`
	Limitations    string     `json:"limitations"`
	// Register is the text register the task's brief and return are written in, decided by
	// the same target_tasks label that selected the tier. It never changes the tier.
	Register       string `json:"register"`
	RegisterSource string `json:"register_source"`
	MaxTokens      int    `json:"max_tokens,omitempty"`
}

func addModelRouteFlags(fs *flag.FlagSet) modelRouteFlags {
	return modelRouteFlags{
		task:         fs.String("task", "", "Declared target_tasks label for models route"),
		capabilities: fs.String("capabilities", "", "Comma-separated required declared model capabilities for route"),
		input:        fs.Int64("input-tokens", 0, "Estimated input tokens for cost and projected quota checks"),
		output:       fs.Int64("output-tokens", 0, "Estimated output tokens for cost and projected quota checks"),
		usage:        fs.String("usage", "", "Optional bounded JSON capacity snapshot; requires an observation and positive RPM/TPM limits for the selected model"),
	}
}

func validateModelRouteFlags(fs *flag.FlagSet, action string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "task", "capabilities", "input-tokens", "output-tokens", "usage":
			if action != "route" {
				err = fmt.Errorf("--%s requires models route", f.Name)
			}
		case "discover-local", "local-endpoints":
			if action == "route" {
				err = fmt.Errorf("--%s does not apply to offline routing", f.Name)
			}
		case "prune":
			if action != "sync" {
				err = fmt.Errorf("--prune requires models sync")
			}
		}
	})
	return err
}

func handleModelsRoute(ctx context.Context, configPath string, flags modelRouteFlags) error {
	cfg, err := router.LoadRoutingConfigContext(ctx, configPath)
	if err != nil {
		return err
	}
	resolution, err := resolveTaskRegister(ctx, *flags.task)
	if err != nil {
		return err
	}
	request, err := modelRouteRequest(flags)
	if err != nil {
		return err
	}
	limitations := seedOutputEstimate(&request, resolution)
	tracker, captured, err := modelRouteTracker(ctx, cfg, *flags.usage)
	if err != nil {
		return err
	}
	route, err := router.NewModelCapacityArbiter(cfg, tracker).SelectForTask(ctx, request)
	if err != nil {
		return err
	}
	report := modelRouteReport{TaskRoute: route, ConfigSHA256: cfg.SourceSHA256, CapacitySource: "unobserved", Limitations: limitations,
		Register: string(resolution.Register), RegisterSource: resolution.Source, MaxTokens: resolution.MaxTokens}
	if captured != nil {
		report.CapacitySource = "supplied snapshot; counters are not refreshed or expired"
		report.CapturedAt = captured
	}
	return writeModelRouteReport(report)
}

// resolveTaskRegister resolves the text register of task from the manifest of the working
// directory; without a manifest the defaults govern. models route and dogfood repairs share
// it, so both report the same row for the same label.
func resolveTaskRegister(ctx context.Context, task string) (config.Resolution, error) {
	policy, _, err := compiler.LoadRegisterBlock(ctx, ".")
	if err != nil {
		return config.Resolution{}, fmt.Errorf("text register: %w", err)
	}
	return policy.Resolve(config.SurfaceAgent, task), nil
}

// seedOutputEstimate uses the task's configured output budget as the output estimate when
// the caller gave none, and returns the limitations text that says so.
func seedOutputEstimate(request *router.TaskRequest, resolution config.Resolution) string {
	if request.OutputTokens != 0 || resolution.MaxTokens <= 0 {
		return modelRouteLimitations
	}
	request.OutputTokens = int64(resolution.MaxTokens)
	return modelRouteLimitations + fmt.Sprintf("; output estimate seeded from the %d-token budget of %s", resolution.MaxTokens, resolution.Source)
}

func writeModelRouteReport(report modelRouteReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode route: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(data)); err != nil {
		return fmt.Errorf("write route result: %w", err)
	}
	return nil
}

func modelRouteRequest(flags modelRouteFlags) (router.TaskRequest, error) {
	request := router.TaskRequest{Task: *flags.task, InputTokens: *flags.input, OutputTokens: *flags.output, RequireObservedCapacity: *flags.usage != ""}
	if len(*flags.capabilities) > router.MaxRoutingTags*256 {
		return request, fmt.Errorf("capabilities argument exceeds its bound")
	}
	if *flags.capabilities != "" {
		request.Capabilities = strings.Split(*flags.capabilities, ",")
		if len(request.Capabilities) > router.MaxRoutingTags {
			return request, fmt.Errorf("too many requested capabilities")
		}
		for i := 0; i < len(request.Capabilities) && i < router.MaxRoutingTags; i++ {
			request.Capabilities[i] = strings.TrimSpace(request.Capabilities[i])
		}
	}
	return request, nil
}

func modelRouteTracker(ctx context.Context, cfg *router.RoutingConfig, path string) (*router.LimitTracker, *time.Time, error) {
	if path == "" {
		return router.NewLimitTracker(), nil, nil
	}
	snapshot, err := router.LoadUsageSnapshot(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	tracker, err := router.TrackerFromSnapshot(cfg, snapshot)
	if err != nil {
		return nil, nil, err
	}
	return tracker, &snapshot.CapturedAt, nil
}
