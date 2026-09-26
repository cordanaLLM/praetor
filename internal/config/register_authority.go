package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/router"
)

const registerManifestName = ".standards.yaml"

// RegisterAuthority is one parsed manifest snapshot. Its fields stay private so a runtime
// boundary can accept only resolutions produced from bytes it selected canonically.
type RegisterAuthority struct {
	policy          RegisterPolicy
	manifestSHA256  string
	declaredTasks   []string
	manifestData    []byte
	manifestSource  string
	manifestPresent bool
	valid           bool
}

// LoadRegisterAuthority reads the canonical manifest at root once. An absent manifest is
// a real snapshot of the documented default policy and has SHA-256(empty) as its identity.
func LoadRegisterAuthority(ctx context.Context, root string) (RegisterAuthority, error) {
	if ctx == nil || root == "" {
		return RegisterAuthority{}, errors.New("register authority requires context and repository root")
	}
	if err := ctx.Err(); err != nil {
		return RegisterAuthority{}, err
	}
	path := filepath.Join(root, registerManifestName)
	data, err := contextopt.ReadSnapshot(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return registerAuthority(nil, false, path)
	}
	if err != nil {
		return RegisterAuthority{}, fmt.Errorf("read register manifest: %w", err)
	}
	return registerAuthority(data, true, path)
}

// ParseRegisterAuthority parses manifest bytes already selected by a trusted snapshot or
// immutable-source boundary.
func ParseRegisterAuthority(data []byte, source string) (RegisterAuthority, error) {
	return registerAuthority(data, true, source)
}

// AbsentRegisterAuthority returns the canonical no-manifest snapshot.
func AbsentRegisterAuthority() RegisterAuthority {
	authority, err := registerAuthority(nil, false, registerManifestName)
	if err != nil {
		return RegisterAuthority{}
	}
	return authority
}

func registerAuthority(data []byte, present bool, source string) (RegisterAuthority, error) {
	var manifest *Manifest
	var err error
	if present {
		manifest, err = parseManifest(source, data)
		if err != nil {
			return RegisterAuthority{}, err
		}
	}
	digest := sha256.Sum256(data)
	authority := RegisterAuthority{policy: manifest.EffectiveRegister(), manifestSHA256: hex.EncodeToString(digest[:]),
		manifestData: append([]byte(nil), data...), manifestSource: source, manifestPresent: present, valid: true}
	if manifest != nil && manifest.Register != nil {
		authority.declaredTasks = manifest.Register.sortedTaskLabels()
	}
	return authority, nil
}

// Resolve returns a manifest-digest-bound resolution.
func (a RegisterAuthority) Resolve(surface RegisterSurface, task string) (Resolution, error) {
	if !a.valid || !KnownRegisterSurface(surface) {
		return Resolution{}, errors.New("invalid register authority or surface")
	}
	if surface == SurfaceAgent && task != "" && !router.ValidTaskLabel(task) {
		return Resolution{}, fmt.Errorf("invalid register task label %q", task)
	}
	resolution := a.policy.Resolve(surface, task)
	resolution.ManifestSHA256 = a.manifestSHA256
	return resolution, nil
}

// ManifestSHA256 identifies the exact manifest bytes; SHA-256(empty) means absent.
func (a RegisterAuthority) ManifestSHA256() string { return a.manifestSHA256 }

// HasDeclaredTasks reports whether the snapshot wrote task rows that need routing-vocabulary validation.
func (a RegisterAuthority) HasDeclaredTasks() bool { return len(a.declaredTasks) > 0 }

// Manifest reparses an independent manifest from the bound bytes. Callers cannot mutate
// authority state through returned maps, slices or pointers.
func (a RegisterAuthority) Manifest() (*Manifest, error) {
	if !a.valid || !a.manifestPresent {
		return nil, nil
	}
	return parseManifest(a.manifestSource, append([]byte(nil), a.manifestData...))
}

// Policy returns an independent copy for rendering and static inspection.
func (a RegisterAuthority) Policy() RegisterPolicy {
	policy := a.policy
	policy.Surfaces = make(map[RegisterSurface]TextRegister, len(a.policy.Surfaces))
	for surface, register := range a.policy.Surfaces {
		policy.Surfaces[surface] = register
	}
	policy.Tasks = make(map[string]RegisterTask, len(a.policy.Tasks))
	for task, row := range a.policy.Tasks {
		policy.Tasks[task] = row
	}
	return policy
}

// ValidateTaskLabels checks only rows explicitly present in the manifest snapshot.
func (a RegisterAuthority) ValidateTaskLabels(known []string) error {
	declared := RegisterPolicy{Tasks: make(map[string]RegisterTask, len(a.declaredTasks))}
	for _, task := range a.declaredTasks {
		declared.Tasks[task] = a.policy.Tasks[task]
	}
	return declared.ValidateTaskLabels(known)
}
