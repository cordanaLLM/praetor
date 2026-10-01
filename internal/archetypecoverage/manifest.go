// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package archetypecoverage binds every key of the archetype catalog schema to the engine code
// that consumes it, or records that nothing does (#353).
//
// A catalog file declares linters, supply-chain levels and DevContainer features, and the
// effective-policy digest records every one of them. Recording a value is not acting on it: a
// key that nothing reads advertises enforcement the engine does not have. The manifest at
// ManifestFile names, for each key, the functions that read it and what they do with it, or
// says it is unconsumed and why. Verify reads the engine's own Go source, type-checked, and
// fails in both directions, like the HISS coverage catalog (.config/hiss/coverage.yaml): when
// a key has no entry or a listed consumer stops reading it, and when a function starts reading
// a key the manifest calls unconsumed.
package archetypecoverage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ManifestFile is the declarative-coverage manifest, relative to the repository root. It sits
// beside .config/archetypes rather than inside it: the catalog index decodes every YAML file
// there as an archetype, and adoption copies that directory into adopters.
const ManifestFile = ".config/archetype-coverage.yaml"

// Bounds on one manifest (HISS-02).
const (
	maxManifestBytes = 256 * 1024
	maxEntries       = 512
)

// State is what the engine does with one schema key.
type State string

const (
	// StateConsumed: at least one function acts on the value (emits, gates, selects).
	StateConsumed State = "consumed"
	// StateReported: functions only print or return the value; nothing acts on it.
	StateReported State = "reported"
	// StateUnconsumed: no function outside the plumbing reads the value.
	StateUnconsumed State = "unconsumed"
	// StateRefused: a catalog file cannot set the key; the refusing function reads it, and
	// the listed consumers read it from another source such as the repository manifest.
	StateRefused State = "refused"
)

// Kind is what one consumer does with a value.
type Kind string

const (
	// KindActs: the consumer changes an outcome with the value.
	KindActs Kind = "acts"
	// KindReports: the consumer only prints or returns the value.
	KindReports Kind = "reports"
)

// Manifest is the decoded declarative-coverage manifest.
type Manifest struct {
	Version int `yaml:"version"`
	// Schema is the package directory, relative to the module root, that declares the schema
	// types named by Carriers, Containers and each field's Go list.
	Schema string `yaml:"schema"`
	// Carriers are the schema types a key's value travels in. Every field of a carrier must be
	// bound by some key or listed as a container, so a new field cannot carry a value unseen.
	Carriers []string `yaml:"carriers"`
	// Containers are carrier fields that hold a section whose own fields are bound one by one.
	Containers []string `yaml:"containers"`
	// Plumbing are the functions that decode, validate, join or copy a value without acting on
	// it. A read inside one never counts as a consumer.
	Plumbing []Plumbing `yaml:"plumbing"`
	Fields   []Field    `yaml:"fields"`
}

// Plumbing is one function that carries values without consuming them.
type Plumbing struct {
	Func string `yaml:"func"`
	Role string `yaml:"role"`
}

// Field binds one schema key.
type Field struct {
	Key string `yaml:"key"`
	// Go lists the schema struct fields ("Type.Field") the key's value is read from.
	Go        []string   `yaml:"go"`
	State     State      `yaml:"state"`
	Consumers []Consumer `yaml:"consumers"`
	// RefusedBy is the plumbing function that rejects the key in a catalog file (refused only).
	RefusedBy string `yaml:"refused_by"`
	Reason    string `yaml:"reason"`
}

// Consumer is one function that reads a key's value outside the plumbing.
type Consumer struct {
	Func   string `yaml:"func"`
	Kind   Kind   `yaml:"kind"`
	Effect string `yaml:"effect"`
}

// goField matches "Type.Field"; funcName matches "<dir>.<Name>" or "<dir>.<Type>.<Method>", the
// spelling the documentation uses for a Go symbol (internal/state.VerifyStateSync).
var (
	goField  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*$`)
	funcName = regexp.MustCompile(`^[a-z0-9_-]+(?:/[a-z0-9_-]+)*\.[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?$`)
)

// Load reads and validates the manifest under root.
func Load(ctx context.Context, root string) (*Manifest, error) {
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(ManifestFile)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFile, err)
	}
	return Parse(data)
}

// Parse decodes one manifest document with unknown keys refused and validates it.
func Parse(data []byte) (*Manifest, error) {
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", ManifestFile, maxManifestBytes)
	}
	var manifest Manifest
	if err := util.DecodeYAMLStrict(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if err := manifest.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	return &manifest, nil
}

func (m *Manifest) validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported version %d", m.Version)
	}
	if strings.TrimSpace(m.Schema) == "" || len(m.Carriers) == 0 || len(m.Fields) == 0 {
		return errors.New("schema, carriers and fields are required")
	}
	if len(m.Carriers)+len(m.Containers)+len(m.Plumbing)+len(m.Fields) > maxEntries {
		return fmt.Errorf("more than %d entries", maxEntries)
	}
	problems := m.namingProblems()
	for i := range m.Fields {
		if err := m.Fields[i].validate(); err != nil {
			problems = append(problems, fmt.Errorf("key %q: %w", m.Fields[i].Key, err))
		}
	}
	return errors.Join(problems...)
}

// namingProblems checks the spelling of every container and plumbing entry.
func (m *Manifest) namingProblems() []error {
	var problems []error
	for _, container := range m.Containers {
		if !goField.MatchString(container) {
			problems = append(problems, fmt.Errorf("container %q is not Type.Field", container))
		}
	}
	for _, plumbing := range m.Plumbing {
		if !funcName.MatchString(plumbing.Func) || strings.TrimSpace(plumbing.Role) == "" {
			problems = append(problems, fmt.Errorf("plumbing %q needs a <dir>.<Func> name and a role", plumbing.Func))
		}
	}
	return problems
}

// validate checks one entry's shape and that its state agrees with its consumers.
func (f *Field) validate() error {
	if strings.TrimSpace(f.Key) == "" || len(f.Go) == 0 {
		return errors.New("an entry needs a key and at least one Go field")
	}
	for _, name := range f.Go {
		if !goField.MatchString(name) {
			return fmt.Errorf("go field %q is not Type.Field", name)
		}
	}
	acts, err := f.validateConsumers()
	if err != nil {
		return err
	}
	return f.validateState(acts)
}

// validateConsumers checks each consumer and counts those that act.
func (f *Field) validateConsumers() (int, error) {
	acts := 0
	for _, consumer := range f.Consumers {
		if !funcName.MatchString(consumer.Func) || strings.TrimSpace(consumer.Effect) == "" {
			return 0, fmt.Errorf("consumer %q needs a <dir>.<Func> name and an effect", consumer.Func)
		}
		switch consumer.Kind {
		case KindActs:
			acts++
		case KindReports:
		default:
			return 0, fmt.Errorf("consumer %s has kind %q, want acts or reports", consumer.Func, consumer.Kind)
		}
	}
	return acts, nil
}

// validateState holds each state to its contract.
func (f *Field) validateState(acts int) error {
	needsReason := f.State == StateUnconsumed || f.State == StateRefused
	if needsReason && strings.TrimSpace(f.Reason) == "" {
		return fmt.Errorf("state %s needs a reason", f.State)
	}
	if (f.RefusedBy != "") != (f.State == StateRefused) {
		return errors.New("refused_by is required for state refused and allowed for no other")
	}
	return f.validateConsumerCount(acts)
}

// validateConsumerCount checks the consumers each state requires or forbids.
func (f *Field) validateConsumerCount(acts int) error {
	switch f.State {
	case StateConsumed:
		if acts == 0 {
			return errors.New("state consumed needs a consumer of kind acts")
		}
	case StateReported:
		if len(f.Consumers) == 0 || acts > 0 {
			return errors.New("state reported needs consumers, all of kind reports")
		}
	case StateUnconsumed:
		if len(f.Consumers) > 0 {
			return errors.New("state unconsumed lists no consumers")
		}
	case StateRefused:
		if !funcName.MatchString(f.RefusedBy) {
			return fmt.Errorf("refused_by %q is not <dir>.<Func>", f.RefusedBy)
		}
	default:
		return fmt.Errorf("state %q, want consumed, reported, unconsumed or refused", f.State)
	}
	return nil
}
