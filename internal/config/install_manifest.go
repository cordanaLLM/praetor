// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The install manifest is the one persisted settings selection. `workstation install` and
// `workstation update` write it; `hook`, `clients` and `workstation` read it through
// SelectOperatorSettings. `audit` and `gate` never read it: they take explicit flags only.

// InstallManifestVersion is the schema version this reader accepts.
const InstallManifestVersion = 1

// Environment variables that select operator settings for hook, clients and workstation.
const (
	FleetConfigEnv       = "PRAETOR_FLEET_CONFIG"
	WorkstationConfigEnv = "PRAETOR_WORKSTATION_CONFIG"
)

// Where a selected settings document came from.
const (
	SettingsFromFlag        = "flag"
	SettingsFromEnvironment = "environment"
	SettingsFromManifest    = "install-manifest"
	SettingsNotConfigured   = "not-configured"
)

// InstallManifest records what `workstation install|update` put on this host and which
// settings documents it used. Paths are host paths; digests bind the settings bytes.
type InstallManifest struct {
	Version      int                        `json:"version"`
	EngineCommit string                     `json:"engine_commit"`
	InstalledAt  string                     `json:"installed_at"`
	BinDir       string                     `json:"bin_dir"`
	Binaries     map[string]InstalledBinary `json:"binaries"`
	Previous     *InstalledPrevious         `json:"previous,omitempty"`
	Settings     InstalledSettings          `json:"settings"`
	LastUpdate   *InstallAttempt            `json:"last_update,omitempty"`
}

// InstalledBinary is one installed executable.
type InstalledBinary struct {
	SHA256 string `json:"sha256"`
}

// InstalledPrevious is the install a rollback restores.
type InstalledPrevious struct {
	EngineCommit string `json:"engine_commit"`
	Backup       string `json:"backup"`
}

// InstalledSettings names the settings documents the install used.
type InstalledSettings struct {
	Fleet       *InstalledDocument `json:"fleet,omitempty"`
	Workstation *InstalledDocument `json:"workstation,omitempty"`
}

// InstalledDocument is a settings document and the digest of its bytes at install time.
type InstalledDocument struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// InstallAttempt is the outcome of the last update run.
type InstallAttempt struct {
	At     string `json:"at"`
	Target string `json:"target"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

var (
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	installedBinaries = []string{"praetor-lsp", "praetor-mcp", "praetorctl"}
	attemptResults    = []string{"updated", "up-to-date", "held", "rolled-back", "failed"}
)

// DefaultInstallManifestPath is install.json under the per-user configuration directory,
// beside the receipt signing key.
func DefaultInstallManifestPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "praetor", "install.json"), nil
}

// ReadInstallManifest reads and validates a manifest: a bounded regular file, strict JSON
// without unknown or duplicate members, every field in its documented form.
func ReadInstallManifest(ctx context.Context, path string) (InstallManifest, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return InstallManifest{}, fmt.Errorf("read install manifest %s: %w", path, err)
	}
	return DecodeInstallManifest(data)
}

// DecodeInstallManifest validates manifest bytes; see ReadInstallManifest.
func DecodeInstallManifest(data []byte) (InstallManifest, error) {
	var manifest InstallManifest
	if err := json.Unmarshal(data, &manifest, json.RejectUnknownMembers(true)); err != nil {
		return InstallManifest{}, fmt.Errorf("decode install manifest: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return InstallManifest{}, fmt.Errorf("install manifest: %w", err)
	}
	return manifest, nil
}

// WriteInstallManifest validates manifest and writes it to path atomically: a temp file
// in the same directory, then a rename, so a reader (SelectOperatorSettings included)
// never observes a partially written manifest. The parent directory is created private to
// the owner when missing. `workstation install` and `workstation update` are the only
// callers; nothing else persists this file.
func WriteInstallManifest(path string, manifest InstallManifest) (err error) {
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("install manifest: %w", err)
	}
	data, err := json.Marshal(manifest, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encode install manifest: %w", err)
	}
	dir := filepath.Dir(path)
	if err := util.MkdirSecure(dir, 0o700); err != nil {
		return fmt.Errorf("create install manifest directory %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".install-*.json.tmp")
	if err != nil {
		return fmt.Errorf("stage install manifest in %s: %w", dir, err)
	}
	tempPath := temp.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = fmt.Errorf("remove staged install manifest %s: %w", tempPath, removeErr)
		}
	}()
	if writeErr := writeInstallManifestTemp(temp, data); writeErr != nil {
		return writeErr
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("place install manifest at %s: %w", path, err)
	}
	return nil
}

// writeInstallManifestTemp writes and closes the staged manifest file with owner-only
// permissions, isolating the fallible I/O steps WriteInstallManifest's deferred cleanup
// depends on having a settled file handle for. The close always runs, and its error is
// only reported when nothing earlier already failed.
func writeInstallManifestTemp(temp *os.File, data []byte) (err error) {
	defer func() {
		if cerr := temp.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close staged install manifest: %w", cerr)
		}
	}()
	if _, writeErr := temp.Write(append(data, '\n')); writeErr != nil {
		return fmt.Errorf("write install manifest: %w", writeErr)
	}
	if chmodErr := temp.Chmod(0o600); chmodErr != nil {
		return fmt.Errorf("set install manifest permissions: %w", chmodErr)
	}
	return nil
}

func (m InstallManifest) validate() error {
	for _, check := range []func() error{m.validateInstall, m.validateBinaries, m.validateSettings, m.LastUpdate.validate} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (m InstallManifest) validateInstall() error {
	if m.Version != InstallManifestVersion {
		return fmt.Errorf("version must be %d", InstallManifestVersion)
	}
	if !commitPattern.MatchString(m.EngineCommit) || !rfc3339(m.InstalledAt) {
		return errors.New("engine_commit must be a 40-character lowercase commit id and installed_at an RFC 3339 time")
	}
	if !util.CleanAbsoluteLiteral(m.BinDir, maxSettingValueBytes) {
		return errors.New("bin_dir must be a clean absolute path")
	}
	if p := m.Previous; p != nil && (!commitPattern.MatchString(p.EngineCommit) || !util.CleanAbsoluteLiteral(p.Backup, maxSettingValueBytes)) {
		return errors.New("previous needs a commit id and a clean absolute backup directory")
	}
	return nil
}

func (m InstallManifest) validateBinaries() error {
	if len(m.Binaries) == 0 || len(m.Binaries) > len(installedBinaries) {
		return fmt.Errorf("binaries must name 1..%d executables", len(installedBinaries))
	}
	for name, binary := range m.Binaries {
		if !slices.Contains(installedBinaries, name) || !sha256Pattern.MatchString(binary.SHA256) {
			return fmt.Errorf("binary %q must be one of %v with a SHA-256 digest", name, installedBinaries)
		}
	}
	return nil
}

func (m InstallManifest) validateSettings() error {
	for _, document := range []*InstalledDocument{m.Settings.Fleet, m.Settings.Workstation} {
		if document != nil && (!util.CleanAbsoluteLiteral(document.Path, maxSettingValueBytes) || !sha256Pattern.MatchString(document.SHA256)) {
			return errors.New("a settings document needs a clean absolute path and a SHA-256 digest")
		}
	}
	return nil
}

func (a *InstallAttempt) validate() error {
	if a == nil {
		return nil
	}
	if !rfc3339(a.At) || !commitPattern.MatchString(a.Target) || !slices.Contains(attemptResults, a.Result) {
		return fmt.Errorf("last_update needs an RFC 3339 time, a commit id and a result among %v", attemptResults)
	}
	if !util.LiteralString(a.Reason, maxSettingValueBytes) {
		return errors.New("last_update reason must be a bounded literal")
	}
	return nil
}

func rfc3339(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

// SettingsRequest carries what a command knows about its settings selection. Getenv is
// injected so the resolver never reads process state itself; nil means no environment.
type SettingsRequest struct {
	FleetFlag       string
	WorkstationFlag string
	Getenv          func(string) string
	ManifestPath    string
}

// SettingsDocument is one selected document; Path is empty when not configured.
type SettingsDocument struct {
	Path   string
	Origin string
}

// SettingsSelection is the fleet and workstation documents for hook, clients and workstation.
type SettingsSelection struct {
	Fleet       SettingsDocument
	Workstation SettingsDocument
}

// SelectOperatorSettings resolves each document in order: the command flag, then the
// environment variable, then the path the install manifest recorded, then not configured
// (built-in defaults). A recorded document whose bytes no longer match the recorded digest is
// an error, never a silent fallback. A missing manifest means not configured.
func SelectOperatorSettings(ctx context.Context, request SettingsRequest) (SettingsSelection, error) {
	if ctx == nil {
		return SettingsSelection{}, errors.New("settings selection requires a context")
	}
	selection := SettingsSelection{
		Fleet:       explicitDocument(request.FleetFlag, request.Getenv, FleetConfigEnv),
		Workstation: explicitDocument(request.WorkstationFlag, request.Getenv, WorkstationConfigEnv),
	}
	if selection.Fleet.Path != "" && selection.Workstation.Path != "" || request.ManifestPath == "" {
		return selection, nil
	}
	if err := selection.fillFromManifest(ctx, request.ManifestPath); err != nil {
		return SettingsSelection{}, err
	}
	return selection, nil
}

// fillFromManifest takes each document not chosen explicitly from the install manifest.
func (s *SettingsSelection) fillFromManifest(ctx context.Context, path string) error {
	manifest, err := ReadInstallManifest(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Fleet.Path == "" {
		if s.Fleet, err = recordedDocument(ctx, "fleet", manifest.Settings.Fleet); err != nil {
			return err
		}
	}
	if s.Workstation.Path == "" {
		s.Workstation, err = recordedDocument(ctx, "workstation", manifest.Settings.Workstation)
	}
	return err
}

func explicitDocument(flag string, getenv func(string) string, variable string) SettingsDocument {
	if flag != "" {
		return SettingsDocument{Path: flag, Origin: SettingsFromFlag}
	}
	if getenv != nil {
		if value := getenv(variable); value != "" {
			return SettingsDocument{Path: value, Origin: SettingsFromEnvironment}
		}
	}
	return SettingsDocument{Origin: SettingsNotConfigured}
}

func recordedDocument(ctx context.Context, name string, document *InstalledDocument) (SettingsDocument, error) {
	if document == nil {
		return SettingsDocument{Origin: SettingsNotConfigured}, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, document.Path)
	if err != nil {
		return SettingsDocument{}, fmt.Errorf("%s settings recorded by the install manifest: %w", name, err)
	}
	if policyDigest(data) != document.SHA256 {
		return SettingsDocument{}, fmt.Errorf("%s settings %s changed since the install manifest recorded them; run workstation update or select the file explicitly", name, document.Path)
	}
	return SettingsDocument{Path: document.Path, Origin: SettingsFromManifest}, nil
}
