package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

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
		}
	})
	return err
}

func handleModelsRoute(ctx context.Context, configPath string, flags modelRouteFlags) error {
	cfg, err := router.LoadRoutingConfigContext(ctx, configPath)
	if err != nil {
		return err
	}
	request, err := modelRouteRequest(flags)
	if err != nil {
		return err
	}
	tracker, captured, err := modelRouteTracker(ctx, cfg, *flags.usage)
	if err != nil {
		return err
	}
	route, err := router.NewModelCapacityArbiter(cfg, tracker).SelectForTask(ctx, request)
	if err != nil {
		return err
	}
	report := modelRouteReport{TaskRoute: route, ConfigSHA256: cfg.SourceSHA256, CapacitySource: "unobserved",
		Limitations: "Configured cost estimates and task/capability declarations only; no current-price, measured latency/quality, provider availability, concurrency reservation or dispatch claim"}
	if captured != nil {
		report.CapacitySource = "supplied snapshot; counters are not refreshed or expired"
		report.CapturedAt = captured
	}
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
