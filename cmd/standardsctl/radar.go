// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/radar"
	"github.com/cordanaLLM/praetor/internal/util"
)

// radarTimeout bounds one radar command (HISS-02).
const radarTimeout = 2 * time.Minute

// radarDigestPerm is the permission of a written digest: a report other tools read.
const radarDigestPerm = 0o644

// runRadar dispatches the research and upstream radar commands (#818).
func runRadar(args []string) error {
	if len(args) < 1 {
		printRadarUsage()
		return nil
	}
	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := commandContext(radarTimeout)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printRadarUsage()
		return nil
	case "validate":
		return runRadarValidate(ctx, subArgs)
	case "collect":
		return runRadarCollect(ctx, subArgs)
	default:
		return fmt.Errorf("unknown radar subcommand: %s", sub)
	}
}

func printRadarUsage() {
	fmt.Println("Usage: praetorctl radar <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  validate [--path=.] [--registry=<file>]  Validate the source registry .standards.yaml names in radar.registry")
	fmt.Println("  collect --fixture=<dir> --now=<time> --out=<file> [--days=7] [--path=.] [--registry=<file>]")
	fmt.Println("                                           Collect the window [now - days, now) into a Markdown digest")
	fmt.Println("\ncollect reads planted source files only: <id>.xml per feed, <id>.json per github_repo releases")
	fmt.Println("listing. Network collection is not implemented yet. --now is an RFC 3339 timestamp or YYYY-MM-DD.")
}

func runRadarValidate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("radar validate", flag.ContinueOnError)
	path := fs.String("path", ".", "Repository root whose .standards.yaml declares radar.registry")
	registryFile := fs.String("registry", "", "Registry file to validate instead of the declared one")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 0 {
		return fmt.Errorf("radar validate takes no positional arguments, got %q", positional)
	}
	registry, label, err := loadRadarRegistry(ctx, *path, *registryFile)
	if err != nil {
		return err
	}
	fmt.Printf("[PASS] radar registry %s: %s\n", label, describeRadarSources(registry))
	return nil
}

// loadRadarRegistry loads the registry named by --registry, or else the one the manifest at root
// declares. A manifest without a radar section is an error naming both ways to fix it, never a
// fallback to a default file.
func loadRadarRegistry(ctx context.Context, root, registryFile string) (*radar.Registry, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if registryFile != "" {
		registry, err := radar.LoadRegistry(filepath.Dir(registryFile), filepath.Base(registryFile))
		return registry, registryFile, err
	}
	rel, declared, err := config.RepositoryRadarRegistry(root)
	if err != nil {
		return nil, "", fmt.Errorf("radar: %w", err)
	}
	if !declared {
		return nil, "", fmt.Errorf("radar: %s declares no radar section; declare radar.registry there or pass --registry",
			filepath.Join(root, config.ManifestFileName))
	}
	registry, err := radar.LoadRegistry(root, filepath.FromSlash(rel))
	return registry, rel, err
}

// describeRadarSources counts a registry's sources by kind.
func describeRadarSources(registry *radar.Registry) string {
	feeds, repos := 0, 0
	for i := 0; i < len(registry.Sources) && i < radar.MaxSources; i++ {
		if registry.Sources[i].Kind == radar.KindFeed {
			feeds++
		} else {
			repos++
		}
	}
	return fmt.Sprintf("%d sources (feed: %d, github_repo: %d)", len(registry.Sources), feeds, repos)
}

// radarCollectRequest is one parsed `radar collect` call.
type radarCollectRequest struct {
	path, registry, fixture, out string
	window                       radar.Window
}

func parseRadarCollect(args []string) (radarCollectRequest, error) {
	fs := flag.NewFlagSet("radar collect", flag.ContinueOnError)
	path := fs.String("path", ".", "Repository root whose .standards.yaml declares radar.registry")
	registryFile := fs.String("registry", "", "Registry file to collect instead of the declared one")
	fixture := fs.String("fixture", "", "Directory of planted source files: <id>.xml per feed, <id>.json per github_repo")
	now := fs.String("now", "", "Exclusive end of the window: an RFC 3339 timestamp or YYYY-MM-DD (midnight UTC)")
	days := fs.Int("days", 7, "Window length in days, 1 to 366")
	out := fs.String("out", "", "File the Markdown digest is written to; it is empty when nothing is new")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return radarCollectRequest{}, err
	}
	switch {
	case len(positional) > 0:
		return radarCollectRequest{}, fmt.Errorf("radar collect takes no positional arguments, got %q", positional)
	case *fixture == "":
		return radarCollectRequest{}, errors.New("radar collect: network collection is not implemented yet (#818); pass --fixture=<dir>")
	case *out == "":
		return radarCollectRequest{}, errors.New("radar collect: --out=<file> is required")
	}
	if info, statErr := os.Stat(*fixture); statErr != nil || !info.IsDir() {
		return radarCollectRequest{}, fmt.Errorf("radar collect: --fixture %s is not a directory", *fixture)
	}
	instant, err := radar.ParseNow(*now)
	if err != nil {
		return radarCollectRequest{}, err
	}
	window, err := radar.NewWindow(instant, *days)
	if err != nil {
		return radarCollectRequest{}, err
	}
	return radarCollectRequest{path: *path, registry: *registryFile, fixture: *fixture, out: *out, window: window}, nil
}

// runRadarCollect collects the window from the fixture directory and writes the digest. The
// digest is written even when every source failed, so the failures are on record; that run then
// exits non-zero.
func runRadarCollect(ctx context.Context, args []string) error {
	req, err := parseRadarCollect(args)
	if err != nil {
		return err
	}
	registry, _, err := loadRadarRegistry(ctx, req.path, req.registry)
	if err != nil {
		return err
	}
	fmt.Printf("Read path: planted fixtures in %s (no network)\n", req.fixture)
	digest, collectErr := radar.Collect(ctx, registry, radar.FixtureReader{Dir: req.fixture}, req.window)
	if collectErr != nil && !errors.Is(collectErr, radar.ErrAllSourcesFailed) {
		return collectErr
	}
	data := radar.Render(digest)
	if err := util.WriteFileAtomic(req.out, data, radarDigestPerm); err != nil {
		return fmt.Errorf("radar collect: write %s: %w", req.out, err)
	}
	if collectErr != nil {
		return fmt.Errorf("[FAIL] %w; the digest at %s names each", collectErr, req.out)
	}
	printRadarSummary(req, digest, len(data))
	return nil
}

// printRadarSummary reports what the digest holds and names every failed source.
func printRadarSummary(req radarCollectRequest, digest radar.Digest, size int) {
	window := req.window.Since.Format(time.RFC3339) + " <= t < " + req.window.Now.Format(time.RFC3339)
	if size == 0 {
		fmt.Printf("[PASS] nothing new in %s; wrote an empty digest to %s\n", window, req.out)
		return
	}
	items := 0
	for i := 0; i < len(digest.Sections); i++ {
		items += len(digest.Sections[i].Items)
	}
	fmt.Printf("[PASS] wrote %s: %d items from %d sources in %s\n", req.out, items, len(digest.Sections), window)
	if len(digest.Failed) == 0 {
		return
	}
	failed := make([]string, 0, len(digest.Failed))
	for i := 0; i < len(digest.Failed); i++ {
		failed = append(failed, digest.Failed[i].Source.ID)
	}
	fmt.Printf("  %d of %d sources failed and are named in the digest: %s\n",
		len(digest.Failed), digest.Read+len(digest.Failed), strings.Join(failed, ", "))
}
