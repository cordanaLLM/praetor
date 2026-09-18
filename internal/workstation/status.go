// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// StatusOptions configures Status. Checkout and BinDir are optional: status still reports
// what it can from the manifest alone. ClientEnv drives the per-client root resolution
// (C1); its zero value still runs, reporting every client as unresolved.
type StatusOptions struct {
	Checkout     string
	BinDir       string
	ManifestPath string
	ClientEnv    clientsetup.Env
}

// ClientStatus is one known client's configuration-root presence, resolved through C1.
type ClientStatus struct {
	Client      clientid.ID `json:"client"`
	Root        string      `json:"root,omitempty"`
	ConfigFound bool        `json:"config_found"`
	Reason      string      `json:"reason,omitempty"`
}

// StatusReport answers "which commit is this workstation on".
type StatusReport struct {
	ManifestPath   string                  `json:"manifest_path"`
	Installed      bool                    `json:"installed"`
	Manifest       *config.InstallManifest `json:"manifest,omitempty"`
	ManifestSHA256 string                  `json:"manifest_sha256,omitempty"`
	CheckoutHead   string                  `json:"checkout_head,omitempty"`
	UpToDate       bool                    `json:"up_to_date"`
	LockHeld       bool                    `json:"lock_held"`
	Clients        []ClientStatus          `json:"clients"`
}

// Status reports the installed commit and manifest digest against ManifestPath, the
// checkout's current HEAD when Checkout is given, whether an installation lock is held,
// and per-client configuration-root presence (C1). It never takes the lock itself.
func Status(ctx context.Context, opts StatusOptions) (StatusReport, error) {
	if ctx == nil {
		return StatusReport{}, errors.New("workstation: status requires a context")
	}
	opts, err := resolveStatusManifestPath(opts)
	if err != nil {
		return StatusReport{}, err
	}
	report := StatusReport{ManifestPath: opts.ManifestPath, Clients: clientStatuses(opts.ClientEnv)}
	if err := fillManifestStatus(ctx, &report, opts.ManifestPath); err != nil {
		return StatusReport{}, err
	}
	if opts.Checkout != "" {
		head, err := engineCommit(ctx, opts.Checkout)
		if err != nil {
			return StatusReport{}, err
		}
		report.CheckoutHead = head
		report.UpToDate = report.Manifest != nil && report.Manifest.EngineCommit == head
	}
	if binDir := effectiveBinDir(opts, report.Manifest); binDir != "" {
		report.LockHeld = LockHeld(binDir)
	}
	return report, nil
}

func resolveStatusManifestPath(opts StatusOptions) (StatusOptions, error) {
	if opts.ManifestPath != "" {
		return opts, nil
	}
	path, err := config.DefaultInstallManifestPath()
	if err != nil {
		return opts, fmt.Errorf("workstation: resolve default manifest path: %w", err)
	}
	opts.ManifestPath = path
	return opts, nil
}

func fillManifestStatus(ctx context.Context, report *StatusReport, path string) error {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workstation: read install manifest %s: %w", path, err)
	}
	manifest, err := config.DecodeInstallManifest(data)
	if err != nil {
		return fmt.Errorf("workstation: decode install manifest %s: %w", path, err)
	}
	report.Installed = true
	report.Manifest = &manifest
	report.ManifestSHA256 = digestBytes(data)
	return nil
}

func effectiveBinDir(opts StatusOptions, manifest *config.InstallManifest) string {
	if opts.BinDir != "" {
		return opts.BinDir
	}
	if manifest != nil {
		return manifest.BinDir
	}
	return ""
}

func clientStatuses(env clientsetup.Env) []ClientStatus {
	known := clientid.Known()
	statuses := make([]ClientStatus, 0, len(known))
	for _, id := range known {
		statuses = append(statuses, clientStatus(id, env))
	}
	return statuses
}

func clientStatus(id clientid.ID, env clientsetup.Env) ClientStatus {
	root, err := clientsetup.Root(id, clientsetup.ScopeGlobal, env)
	if err != nil {
		return ClientStatus{Client: id, Reason: err.Error()}
	}
	found := env.DirExists != nil && env.DirExists(root)
	return ClientStatus{Client: id, Root: root, ConfigFound: found}
}
