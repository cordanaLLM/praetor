package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// prepareConnections exports one private profile into the existing shared
// registry and Responses provider configuration. It never reads a credential.
func prepareConnections(args []string) error {
	flags := flag.NewFlagSet("clients connect", flag.ContinueOnError)
	profilePath := flags.String("profile", "", "Private gateway, provider and optional memory connection profile")
	output := flags.String("out", "", "New private connection artifact directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *output == "" {
		return errors.New("usage: praetorctl clients connect --profile FILE --out NEW_DIRECTORY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile, err := readConnectionProfile(ctx, *profilePath)
	if err != nil {
		return err
	}
	files, err := clientsetup.ConnectionArtifacts(ctx, profile)
	if err != nil {
		return err
	}
	if err := contextopt.WriteArtifacts(ctx, *output, files); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, "Connection artifacts prepared; use clients prepare/apply with registry.json and clients bind-memory for the repository mapping. Connectivity is unverified.")
	return err
}

func readConnectionProfile(ctx context.Context, path string) (clientsetup.ConnectionProfile, error) {
	raw, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return clientsetup.ConnectionProfile{}, fmt.Errorf("read connection profile: %w", err)
	}
	return clientsetup.DecodeConnectionProfile(ctx, raw)
}

// bindClientMemory updates only the selected project's bank mapping. Publication
// uses the same exact-backup, compare-and-swap and readback as client settings.
func bindClientMemory(args []string) error {
	flags := flag.NewFlagSet("clients bind-memory", flag.ContinueOnError)
	profilePath := flags.String("profile", "", "Private connection profile with a memory binding")
	target := flags.String("target", "", "Existing Hindsight coding-agent configuration")
	output := flags.String("out", "", "New private backup and candidate directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *target == "" || *output == "" {
		return errors.New("usage: praetorctl clients bind-memory --profile FILE --target FILE --out NEW_DIRECTORY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile, err := readConnectionProfile(ctx, *profilePath)
	if err != nil {
		return err
	}
	if profile.Memory == nil {
		return errors.New("connection profile has no memory binding")
	}
	before, err := contextopt.ReadSnapshot(ctx, *target)
	if err != nil {
		return err
	}
	plan, err := clientsetup.BuildMemoryPlan(ctx, *profile.Memory, before)
	if err != nil {
		return err
	}
	return publishClientPlan(ctx, plan, *target, *output, before, true)
}
