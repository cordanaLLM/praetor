package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/standards/internal/release"
)

func runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	ver := fs.String("version", "", "Target release version (e.g. v1.1.0)")
	date := fs.String("date", "", "Release date (YYYY-MM-DD)")
	skipVerify := fs.Bool("skip-verify", false, "Skip make verify-all check")
	skipClean := fs.Bool("skip-clean", false, "Skip working tree cleanliness check")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if *ver == "" {
		return fmt.Errorf("version is required: standardsctl release --version=vX.Y.Z")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	opts := release.ReleaseOptions{
		RepoPath:   ".",
		Version:    *ver,
		Date:       *date,
		SkipVerify: *skipVerify,
		SkipClean:  *skipClean,
	}

	if err := release.PrepareRelease(ctx, opts); err != nil {
		return err
	}

	fmt.Printf("[SUCCESS] Release %s prepared and changelog rendered.\n", *ver)
	return nil
}
