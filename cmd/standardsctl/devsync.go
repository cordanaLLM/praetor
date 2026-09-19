package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/devsync"
)

// devsyncTimeout bounds a whole devsync run; every rclone call carries its own tighter bound.
const devsyncTimeout = 24 * time.Hour

// defaultMaxArchiveSize is push's --max-archive-size default: a unit whose measured source
// exceeds this is skipped, not uploaded. "0" or "none" disables the cap.
const defaultMaxArchiveSize = "2GiB"

// rcloneBinaryEnv selects the rclone executable, so tests and unusual installs can point
// devsync at a specific binary. Unset, rclone is found on PATH.
const rcloneBinaryEnv = "PRAETOR_RCLONE"

func runDevsync(args []string) error {
	if len(args) < 1 {
		printDevsyncUsage()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), devsyncTimeout)
	defer cancel()
	switch args[0] {
	case "-h", "--help", "help":
		printDevsyncUsage()
		return nil
	case "init":
		return runDevsyncInit(ctx, args[1:])
	case "push":
		return runDevsyncPush(ctx, args[1:])
	case "pull":
		return runDevsyncPull(ctx, args[1:])
	case "ls":
		return runDevsyncList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown devsync subcommand: %s", args[0])
	}
}

func printDevsyncUsage() {
	fmt.Println("Usage: praetorctl devsync <subcommand> [arguments]")
	fmt.Println("\nCopies project folders to Google Drive through rclone and back (development stopgap).")
	fmt.Println("\nSubcommands:")
	fmt.Println("  init [--remote-name=praetor-sync] [--base=gdrive:praetor-sync] [--rclone-config=path]")
	fmt.Println("  push [--dev=<home>/dev] [--remote=praetor-sync:] [--host=<hostname>] [--dry-run]")
	fmt.Println("       [--max-archive-size=2GiB] [--rclone-config=path]")
	fmt.Println("  pull --host=<host> [--remote=praetor-sync:] [--into=<user data dir>/praetor/devsync] [--rclone-config=path]")
	fmt.Println("  ls [--remote=praetor-sync:] [--rclone-config=path]")
}

func devsyncRclone(config string) devsync.Rclone {
	return devsync.Rclone{Binary: os.Getenv(rcloneBinaryEnv), Config: config}
}

func parseDevsyncFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s accepts no positional arguments", fs.Name())
	}
	return nil
}

func runDevsyncInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("devsync init", flag.ContinueOnError)
	name := fs.String("remote-name", devsync.DefaultRemoteName, "rclone crypt remote to create")
	base := fs.String("base", devsync.DefaultBase, "remote folder the crypt remote encrypts into")
	config := fs.String("rclone-config", "", "rclone config file (default: rclone's own)")
	if err := parseDevsyncFlags(fs, args); err != nil {
		return err
	}
	opts := devsync.InitOptions{RemoteName: *name, Base: *base, Rclone: devsyncRclone(*config)}
	if err := devsync.Init(ctx, opts); err != nil {
		return err
	}
	fmt.Printf("Created rclone crypt remote %q over %s with two generated keys.\n", *name, *base)
	fmt.Printf("Every workstation needs this same remote with the same keys: copy the [%s] section of\n", *name)
	fmt.Println("this rclone config file (`rclone config file` prints its path) into the rclone config of")
	fmt.Println("each other workstation instead of running init there.")
	fmt.Println("Keep the keys safe: anyone holding them can read the archives, and without them nothing")
	fmt.Println("can be restored.")
	return nil
}

func runDevsyncPush(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("devsync push", flag.ContinueOnError)
	dev := fs.String("dev", defaultDevDir(), "folder holding the projects")
	remote := fs.String("remote", devsync.DefaultRemote, "remote root")
	host := fs.String("host", "", "folder name for this workstation (default: host name)")
	dryRun := fs.Bool("dry-run", false, "report what would be uploaded without uploading")
	maxArchiveSize := fs.String("max-archive-size", defaultMaxArchiveSize,
		"skip a unit whose measured source exceeds this size, e.g. 2GiB, 500MiB (0 or \"none\" disables the cap)")
	config := fs.String("rclone-config", "", "rclone config file (default: rclone's own)")
	if err := parseDevsyncFlags(fs, args); err != nil {
		return err
	}
	sizeCap, err := devsync.ParseSize(*maxArchiveSize)
	if err != nil {
		return fmt.Errorf("--max-archive-size: %w", err)
	}
	opts, err := devsyncPushOptions(*dev, *remote, *host, *config)
	if err != nil {
		return err
	}
	opts.DryRun, opts.MaxArchiveSize = *dryRun, sizeCap
	_, err = devsync.Push(ctx, opts)
	return err
}

func devsyncPushOptions(dev, remote, host, config string) (devsync.PushOptions, error) {
	if host == "" {
		name, err := os.Hostname()
		if err != nil {
			return devsync.PushOptions{}, fmt.Errorf("resolve host name (pass --host): %w", err)
		}
		host = name
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return devsync.PushOptions{}, fmt.Errorf("resolve home directory: %w", err)
	}
	statePath, err := devsync.DefaultStatePath()
	if err != nil {
		return devsync.PushOptions{}, err
	}
	return devsync.PushOptions{
		DevDir: dev, HomeDir: home, Remote: remote, Host: host, StatePath: statePath,
		Rclone: devsyncRclone(config), Out: os.Stdout,
	}, nil
}

func runDevsyncPull(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("devsync pull", flag.ContinueOnError)
	host := fs.String("host", "", "workstation whose archives are restored (required)")
	remote := fs.String("remote", devsync.DefaultRemote, "remote root")
	into := fs.String("into", "", "folder receiving <host>/... (default: <user data dir>/praetor/devsync)")
	config := fs.String("rclone-config", "", "rclone config file (default: rclone's own)")
	if err := parseDevsyncFlags(fs, args); err != nil {
		return err
	}
	if *host == "" {
		return errors.New("devsync pull needs --host")
	}
	target := *into
	if target == "" {
		dir, err := devsync.DefaultPullDir()
		if err != nil {
			return err
		}
		target = dir
	}
	opts := devsync.PullOptions{
		Remote: *remote, Host: *host, Into: target, DevDir: defaultDevDir(), Rclone: devsyncRclone(*config), Out: os.Stdout,
	}
	_, err := devsync.Pull(ctx, opts)
	return err
}

func runDevsyncList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("devsync ls", flag.ContinueOnError)
	remote := fs.String("remote", devsync.DefaultRemote, "remote root")
	config := fs.String("rclone-config", "", "rclone config file (default: rclone's own)")
	if err := parseDevsyncFlags(fs, args); err != nil {
		return err
	}
	archives, err := devsync.List(ctx, devsync.ListOptions{Remote: *remote, Rclone: devsyncRclone(*config)})
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		fmt.Printf("No archives on %s\n", *remote)
		return nil
	}
	for _, archive := range archives {
		name := strings.TrimPrefix(archive.Path, archive.Host+"/")
		fmt.Printf("%-20s %-50s %10s  %s\n", archive.Host, name, devsync.FormatBytes(archive.Size),
			archive.ModTime.Local().Format("2006-01-02 15:04"))
	}
	return nil
}
