// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archetypecoverage

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// verifyTimeout bounds one verification, type-check included (HISS-02).
const verifyTimeout = 5 * time.Minute

// Options selects what Verify checks.
type Options struct {
	// Root is the module root whose Go source is read.
	Root string
	// Manifest is the coverage manifest to check.
	Manifest *Manifest
	// Keys is the closed key set of the schema (config.ArchetypeKeys for the shipped catalog).
	// Every key needs exactly one entry, and every entry must name one of them.
	Keys []string
}

// Report says what was checked as well as what failed: a check that prints only failures
// cannot be told apart from one that read nothing.
type Report struct {
	Keys     int
	Readers  int
	States   map[State]int
	Findings []string
}

// Passed reports whether every binding held.
func (r *Report) Passed() bool { return len(r.Findings) == 0 }

// ErrCoverageContradicted reports a manifest the source contradicts.
var ErrCoverageContradicted = errors.New("archetypecoverage: the manifest does not match what the source reads")

// Verify type-checks the module at opts.Root and compares every binding of opts.Manifest with
// the functions that actually read each key.
func Verify(ctx context.Context, opts Options) (*Report, error) {
	if ctx == nil || opts.Manifest == nil || len(opts.Keys) == 0 {
		return nil, errors.New("archetypecoverage: a context, a manifest and the schema keys are required")
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	index, err := scanModule(ctx, opts.Root, opts.Manifest.Schema)
	if err != nil {
		return nil, err
	}
	check := &checker{manifest: opts.Manifest, index: index, plumbing: map[string]bool{},
		report: &Report{Keys: len(opts.Keys), States: map[State]int{}}}
	check.keys(opts.Keys)
	check.carriers()
	check.plumbingEntries()
	for i := range opts.Manifest.Fields {
		check.field(&opts.Manifest.Fields[i])
	}
	slices.Sort(check.report.Findings)
	return check.report, nil
}

// checker carries one verification.
type checker struct {
	manifest *Manifest
	index    *sourceIndex
	plumbing map[string]bool
	report   *Report
}

func (c *checker) fail(format string, args ...any) {
	c.report.Findings = append(c.report.Findings, fmt.Sprintf(format, args...))
}

// keys holds the entries to the schema in both directions: one entry per key, none for a key
// the schema does not decode.
func (c *checker) keys(keys []string) {
	known := make(map[string]bool, len(keys))
	for _, key := range keys {
		known[key] = true
	}
	seen := map[string]bool{}
	for _, field := range c.manifest.Fields {
		switch {
		case seen[field.Key]:
			c.fail("key %s: bound twice", field.Key)
		case !known[field.Key]:
			c.fail("key %s: the archetype schema decodes no such key", field.Key)
		}
		seen[field.Key] = true
	}
	for _, key := range keys {
		if !seen[key] {
			c.fail("key %s: the schema decodes it and the manifest binds it to no consumer and marks it not unconsumed", key)
		}
	}
}

// carriers requires every field of every carrier type to be bound by a key or listed as a
// container, so a value cannot reach a new field unseen.
func (c *checker) carriers() {
	bound := map[string]bool{}
	for _, field := range c.manifest.Fields {
		for _, name := range field.Go {
			bound[name] = true
		}
	}
	for _, container := range c.manifest.Containers {
		nested, exists := c.index.fields[container]
		if !exists || !nested {
			c.fail("container %s: %s declares no struct-valued field of that name", container, c.manifest.Schema)
		}
		bound[container] = true
	}
	for _, carrier := range c.manifest.Carriers {
		names, exists := c.index.types[carrier]
		if !exists {
			c.fail("carrier %s: %s declares no struct type of that name", carrier, c.manifest.Schema)
		}
		for _, name := range names {
			if !bound[carrier+"."+name] {
				c.fail("carrier field %s.%s: no key binds it and it is not a container", carrier, name)
			}
		}
	}
}

// plumbingEntries requires each plumbing function to exist and to read a bound field, so a
// stale entry cannot hide a reader it no longer names.
func (c *checker) plumbingEntries() {
	reads := map[string]bool{}
	for _, field := range c.manifest.Fields {
		for _, name := range field.Go {
			for reader := range c.index.readers[name] {
				reads[reader] = true
			}
		}
	}
	for _, plumbing := range c.manifest.Plumbing {
		c.plumbing[plumbing.Func] = true
		switch {
		case !c.index.decls[plumbing.Func]:
			c.fail("plumbing %s: no such declaration", plumbing.Func)
		case !reads[plumbing.Func]:
			c.fail("plumbing %s: reads no bound field, so the entry is stale", plumbing.Func)
		}
	}
}

// field compares one entry's declared consumers with the declarations that read its fields
// outside the plumbing, in both directions.
func (c *checker) field(field *Field) {
	c.report.States[field.State]++
	all := c.allReaders(field)
	readers := map[string]bool{}
	for reader := range all {
		if !c.plumbing[reader] {
			readers[reader] = true
		}
	}
	c.report.Readers += len(readers)
	c.compareConsumers(field, readers)
	if field.State == StateRefused && (!c.plumbing[field.RefusedBy] || !all[field.RefusedBy]) {
		c.fail("key %s: refused_by %s is not a plumbing function that reads %s",
			field.Key, field.RefusedBy, strings.Join(field.Go, " or "))
	}
}

// allReaders returns every declaration that reads one of field's Go fields, plumbing included,
// and reports a Go field the schema does not declare.
func (c *checker) allReaders(field *Field) map[string]bool {
	all := map[string]bool{}
	for _, name := range field.Go {
		if _, exists := c.index.fields[name]; !exists {
			c.fail("key %s: %s declares no field %s", field.Key, c.manifest.Schema, name)
		}
		maps.Copy(all, c.index.readers[name])
	}
	return all
}

// compareConsumers fails a listed consumer that does not read the key and a reader the entry
// does not list.
func (c *checker) compareConsumers(field *Field, readers map[string]bool) {
	declared := map[string]bool{}
	for _, consumer := range field.Consumers {
		declared[consumer.Func] = true
		if !readers[consumer.Func] {
			c.fail("key %s: consumer %s does not read %s", field.Key, consumer.Func, strings.Join(field.Go, " or "))
		}
	}
	for _, reader := range slices.Sorted(maps.Keys(readers)) {
		if !declared[reader] {
			c.fail("key %s (%s): %s reads %s and the entry does not list it as a consumer",
				field.Key, field.State, reader, strings.Join(field.Go, " or "))
		}
	}
}
