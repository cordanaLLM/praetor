package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// runClients prepares exact client-specific artifacts from one shared registry.
// Preparation is distinct from native client approval and a successful tool call.
func runClients(args []string) error {
	if len(args) > 0 && args[0] == "apply" {
		return applyClientConfig(args[1:])
	}
	if len(args) == 0 || args[0] != "prepare" {
		return errors.New("usage: praetorctl clients prepare --registry FILE --client CLIENT --out NEW_DIRECTORY [--existing FILE]")
	}
	flags := flag.NewFlagSet("clients prepare", flag.ContinueOnError)
	registryPath := flags.String("registry", "", "Canonical shared stdio server registry")
	client := flags.String("client", "", "Client adapter identifier")
	existing := flags.String("existing", "", "Existing client config to preserve and merge")
	output := flags.String("out", "", "New private artifact directory; parent must exist")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *registryPath == "" || *client == "" || *output == "" {
		return errors.New("registry, client and output directory are required without positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := prepareClient(ctx, *registryPath, *client, *existing)
	if err != nil {
		return err
	}
	return writeClientPlan(ctx, *output, plan)
}

// applyClientConfig takes an explicit destination for project, global, container,
// or mounted GitOps configuration. Native-only adapters retain their own workflow.
func applyClientConfig(args []string) error {
	flags := flag.NewFlagSet("clients apply", flag.ContinueOnError)
	registry := flags.String("registry", "", "Canonical shared stdio server registry")
	client := flags.String("client", "", "Client adapter identifier")
	target := flags.String("target", "", "Exact client configuration destination")
	output := flags.String("out", "", "New private backup and plan directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *registry == "" || *client == "" || *target == "" || *output == "" {
		return errors.New("registry, client, target and new output directory are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	before, exists, err := contextopt.ObserveSnapshot(ctx, *target)
	if err != nil {
		return err
	}
	plan, err := buildClientPlan(ctx, *registry, *client, before)
	if err != nil {
		return err
	}
	return publishClientPlan(ctx, plan, *target, *output, before, exists)
}

func publishClientPlan(ctx context.Context, plan *clientsetup.Plan, target, output string, before []byte, exists bool) error {
	if err := validateClientPublication(plan, target, output, before); err != nil {
		return err
	}
	if err := retainClientPlan(ctx, plan, output, before, exists); err != nil {
		return err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(target), 0o700); err != nil {
		return err
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(ctx, target, plan.Content, options); err != nil {
		return err
	}
	actual, err := contextopt.ReadSnapshot(ctx, target)
	if err != nil {
		return fmt.Errorf("read back client configuration: %w", err)
	}
	if !bytes.Equal(actual, plan.Content) {
		return errors.New("client configuration changed after publication; inspect retained plan and current file")
	}
	_, err = fmt.Fprintf(os.Stdout, "Configured %s at %s; backup and plan: %s. Native trust and tool execution remain unverified.\n", plan.Client, target, output)
	return err
}

func validateClientPublication(plan *clientsetup.Plan, target, output string, before []byte) error {
	if plan == nil {
		return errors.New("client plan is required")
	}
	if plan.Mode == "native" || len(plan.Content) == 0 {
		return errors.New("this adapter requires its native client configuration workflow; use clients prepare")
	}
	if plan.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256(before)) {
		return errors.New("client plan source differs from expected backup snapshot")
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(outputPath, targetPath)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return errors.New("client target must be outside the backup artifact directory")
	}
	return nil
}

func retainClientPlan(ctx context.Context, plan *clientsetup.Plan, output string, before []byte, exists bool) error {
	metadata, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{"plan.json": metadata, plan.ExportName: plan.Content}
	if exists {
		files["config.before"] = before
	}
	return contextopt.WriteArtifacts(ctx, output, files)
}

func prepareClient(ctx context.Context, registryPath, client, existing string) (*clientsetup.Plan, error) {
	var before []byte
	var err error
	if existing != "" {
		before, err = contextopt.ReadSnapshot(ctx, existing)
		if err != nil {
			return nil, fmt.Errorf("read existing client config: %w", err)
		}
	}
	return buildClientPlan(ctx, registryPath, client, before)
}

func buildClientPlan(ctx context.Context, registryPath, client string, before []byte) (*clientsetup.Plan, error) {
	raw, err := contextopt.ReadSnapshot(ctx, registryPath)
	if err != nil {
		return nil, fmt.Errorf("read client registry: %w", err)
	}
	registry, err := clientsetup.DecodeRegistry(ctx, raw)
	if err != nil {
		return nil, err
	}
	return clientsetup.BuildPlan(ctx, registry, clientsetup.Client(client), before)
}

func writeClientPlan(ctx context.Context, output string, plan *clientsetup.Plan) error {
	metadata, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{"plan.json": append(metadata, '\n')}
	if len(plan.Content) != 0 {
		files[plan.ExportName] = plan.Content
	}
	if err := contextopt.WriteArtifacts(ctx, output, files); err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Prepared %s client artifacts in %s; native configuration, trust and tool execution are not yet verified.\n", plan.Client, output)
	return err
}
