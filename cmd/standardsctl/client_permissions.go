package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	permissionStatusUnmanaged  = "unmanaged"
	permissionStatusConfigured = "configured"
	permissionStatusDrifted    = "drifted"
	permissionPlanExport       = "agy-settings.json"
	permissionDocs             = "https://antigravity.google/docs/permissions?tab=cli"
)

type clientPermissionOptions struct {
	action, client, fleet, workstation, manifest, home, target, output string
}

type clientPermissionReport struct {
	Version               int      `json:"version"`
	Client                string   `json:"client"`
	Status                string   `json:"status"`
	Target                string   `json:"target"`
	Managed               bool     `json:"managed"`
	ConfigurationVerified bool     `json:"configuration_verified"`
	RuntimeVerified       bool     `json:"runtime_verified"`
	BeforeCount           int      `json:"before_count"`
	AfterCount            int      `json:"after_count"`
	Missing               []string `json:"missing,omitempty"`
}

func runClientPermissions(args []string) error {
	opts, err := parseClientPermissionOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	permissions, err := loadClientPermissions(ctx, opts)
	if err != nil {
		return err
	}
	target, err := resolvePermissionTarget(opts)
	if err != nil {
		return err
	}
	if !permissions.Manage {
		return writePermissionReport(unmanagedPermissionReport(target))
	}
	if opts.action != "verify" {
		if _, _, err := separatedClientPublicationPaths(ctx, target, opts.output); err != nil {
			return err
		}
	}
	return executeManagedPermissions(ctx, opts, target, permissions)
}

func parseClientPermissionOptions(args []string) (clientPermissionOptions, error) {
	if len(args) == 0 {
		return clientPermissionOptions{}, permissionUsage()
	}
	opts := clientPermissionOptions{action: args[0], client: string(clientsetup.AGY)}
	if !permissionAction(opts.action) {
		return clientPermissionOptions{}, permissionUsage()
	}
	if path, err := config.DefaultInstallManifestPath(); err == nil {
		opts.manifest = path
	}
	flags := flag.NewFlagSet("clients permissions "+opts.action, flag.ContinueOnError)
	flags.StringVar(&opts.client, "client", opts.client, "Client identifier; currently agy")
	flags.StringVar(&opts.fleet, "fleet-config", "", "Fleet operator settings document")
	flags.StringVar(&opts.workstation, "workstation-config", "", "Workstation operator settings document")
	flags.StringVar(&opts.manifest, "manifest", opts.manifest, "Installed settings-selection manifest")
	flags.StringVar(&opts.home, "home", "", "Home directory used for the default AGY settings location")
	flags.StringVar(&opts.target, "target", "", "Exact AGY settings destination")
	flags.StringVar(&opts.output, "out", "", "New private plan and backup directory")
	if err := flags.Parse(args[1:]); err != nil {
		return clientPermissionOptions{}, err
	}
	return validateClientPermissionOptions(opts, flags.NArg())
}

func permissionAction(action string) bool {
	return action == "plan" || action == "apply" || action == "verify"
}

func validateClientPermissionOptions(opts clientPermissionOptions, positional int) (clientPermissionOptions, error) {
	if positional != 0 || opts.client != string(clientsetup.AGY) {
		return clientPermissionOptions{}, errors.New("AGY is the only supported permission adapter; positional arguments are not accepted")
	}
	if opts.action != "verify" && opts.output == "" {
		return clientPermissionOptions{}, errors.New("plan and apply require --out")
	}
	if opts.action == "verify" && opts.output != "" {
		return clientPermissionOptions{}, errors.New("verify does not accept --out")
	}
	if opts.home != "" && opts.target != "" {
		return clientPermissionOptions{}, errors.New("home and target are mutually exclusive")
	}
	return opts, nil
}

func permissionUsage() error {
	return errors.New("usage: praetorctl clients permissions <plan|apply|verify> --client=agy [--fleet-config=<path>] [--workstation-config=<path>] [--manifest=<path>] [--home=<path>|--target=<path>] [--out=<new-directory>]")
}

func loadClientPermissions(ctx context.Context, opts clientPermissionOptions) (config.ClientPermissions, error) {
	selection, err := config.SelectOperatorSettings(ctx, config.SettingsRequest{
		FleetFlag: opts.fleet, WorkstationFlag: opts.workstation,
		Getenv: os.Getenv, ManifestPath: opts.manifest,
	})
	if err != nil {
		return config.ClientPermissions{}, err
	}
	settings, err := config.LoadOperatorSettings(ctx, selection)
	if err != nil {
		return config.ClientPermissions{}, err
	}
	selected, ok := settings.Clients.Selected[clientsetup.AGY]
	if !ok {
		return config.ClientPermissions{}, errors.New("AGY is not selected by the resolved operator settings")
	}
	return selected.Permissions, nil
}

func resolvePermissionTarget(opts clientPermissionOptions) (string, error) {
	if opts.target != "" {
		path, err := filepath.Abs(opts.target)
		if err != nil {
			return "", fmt.Errorf("resolve AGY settings target: %w", err)
		}
		return path, nil
	}
	home, foreign := opts.home, opts.home != ""
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
	}
	path, err := clientsetup.Locate(clientsetup.LocationAGYCLISettings, hostClientEnv(home, foreign))
	if err != nil {
		return "", fmt.Errorf("resolve AGY settings target: %w", err)
	}
	return path, nil
}

func executeManagedPermissions(ctx context.Context, opts clientPermissionOptions, target string, permissions config.ClientPermissions) error {
	before, exists, err := contextopt.ObserveSnapshot(ctx, target)
	if err != nil {
		return fmt.Errorf("observe AGY settings: %w", err)
	}
	if exists && len(before) == 0 {
		return errors.New("existing AGY settings file is empty")
	}
	plan, err := clientsetup.PlanAGYPermissions(ctx, before, permissions)
	if err != nil {
		return err
	}
	publication := permissionPublicationPlan(target, plan)
	switch opts.action {
	case "plan":
		return writeClientPlan(ctx, opts.output, publication)
	case "apply":
		return publishClientPlan(ctx, publication, target, opts.output, before, exists)
	case "verify":
		return verifyPermissionPlan(target, plan)
	default:
		return permissionUsage()
	}
}

func permissionPublicationPlan(target string, plan *clientsetup.AGYPermissionsPlan) *clientsetup.Plan {
	managed, before, after, runtimeVerified := plan.Managed, plan.BeforeCount, plan.AfterCount, false
	return &clientsetup.Plan{
		Client: clientsetup.AGY, Mode: "permissions", Target: target,
		ExportName: permissionPlanExport, SourceSHA256: plan.SourceSHA256,
		Content: plan.Content, Documentation: permissionDocs, Changed: plan.Changed,
		Managed: &managed, BeforeCount: &before, Added: plan.Added,
		AfterCount: &after, RuntimeVerified: &runtimeVerified,
	}
}

func verifyPermissionPlan(target string, plan *clientsetup.AGYPermissionsPlan) error {
	report := clientPermissionReport{
		Version: 1, Client: string(clientsetup.AGY), Target: target, Managed: true,
		Status: permissionStatusConfigured, ConfigurationVerified: !plan.Changed,
		BeforeCount: plan.BeforeCount, AfterCount: plan.AfterCount,
	}
	if plan.Changed {
		report.Status = permissionStatusDrifted
		report.Missing = append([]string(nil), plan.Added...)
	}
	if err := writePermissionReport(report); err != nil {
		return err
	}
	if plan.Changed {
		return errors.New("AGY permission settings are not configured")
	}
	return nil
}

func unmanagedPermissionReport(target string) clientPermissionReport {
	return clientPermissionReport{
		Version: 1, Client: string(clientsetup.AGY), Status: permissionStatusUnmanaged,
		Target: target, Managed: false, ConfigurationVerified: false, RuntimeVerified: false,
	}
}

func writePermissionReport(report clientPermissionReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(raw, '\n'))
	return err
}
