package harvester

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// FleetTopologyFile is the operator-supplied description of the fleet this engine
	// governs. It is deliberately not tracked by the engine: organisation and repository
	// names are operational data belonging to the operational fork, not to the public
	// engine, and a public binary must not disclose a private repository's existence.
	FleetTopologyFile = ".config/fleet-topology.yaml"

	// maxFleetTopologyBytes bounds the read: the document is a short reference list, and
	// an unbounded read of an operator-supplied file is an availability risk.
	maxFleetTopologyBytes = 256 * 1024
	// maxFleetEntries bounds orgs, archetype groups and members per group.
	maxFleetEntries = 512
)

// ErrFleetTopologyAbsent reports that no topology is configured. It is not a failure:
// an engine checkout without operational configuration is the expected public state, and
// callers report it as not configured rather than substituting a built-in fleet.
var ErrFleetTopologyAbsent = errors.New("fleet topology not configured")

// FleetTopology is the declared fleet: which organisations are governed and which
// repositories the operator has classified under each archetype.
type FleetTopology struct {
	Orgs       []string            `yaml:"orgs"`
	Archetypes map[string][]string `yaml:"archetypes"`
}

// LoadFleetTopology reads the operator-supplied topology below rootDir. It returns
// ErrFleetTopologyAbsent when the file does not exist, so an unconfigured engine reports
// that state instead of printing a hardcoded reference that drifts from reality.
func LoadFleetTopology(ctx context.Context, rootDir string) (*FleetTopology, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := util.ConfinePath(rootDir, FleetTopologyFile)
	if err != nil {
		return nil, fmt.Errorf("resolve fleet topology: %w", err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, ErrFleetTopologyAbsent
		}
		return nil, fmt.Errorf("inspect fleet topology: %w", statErr)
	}
	if info.Size() > maxFleetTopologyBytes {
		return nil, fmt.Errorf("fleet topology exceeds %d bytes", maxFleetTopologyBytes)
	}
	data, err := util.ReadFileNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("read fleet topology: %w", err)
	}
	return decodeFleetTopology(data)
}

// decodeFleetTopology parses exactly one YAML document with no unknown fields, so a
// misspelled key is an error rather than a silently empty section.
func decodeFleetTopology(data []byte) (*FleetTopology, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var topology FleetTopology
	if err := decoder.Decode(&topology); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: document is empty", ErrFleetTopologyAbsent)
		}
		return nil, fmt.Errorf("decode fleet topology: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("fleet topology requires exactly one document")
	}
	if err := validateFleetTopology(&topology); err != nil {
		return nil, err
	}
	return &topology, nil
}

// validateFleetTopology enforces the declared bounds and rejects an entry that carries no
// name, which would otherwise print as an empty line in the report.
func validateFleetTopology(topology *FleetTopology) error {
	if len(topology.Orgs) > maxFleetEntries {
		return fmt.Errorf("fleet topology declares more than %d organisations", maxFleetEntries)
	}
	if len(topology.Archetypes) > maxFleetEntries {
		return fmt.Errorf("fleet topology declares more than %d archetypes", maxFleetEntries)
	}
	for i := 0; i < len(topology.Orgs) && i < maxFleetEntries; i++ {
		if topology.Orgs[i] == "" {
			return errors.New("fleet topology declares an empty organisation name")
		}
	}
	for name, members := range topology.Archetypes {
		if name == "" {
			return errors.New("fleet topology declares an empty archetype name")
		}
		if len(members) > maxFleetEntries {
			return fmt.Errorf("archetype %q declares more than %d repositories", name, maxFleetEntries)
		}
		for i := 0; i < len(members) && i < maxFleetEntries; i++ {
			if members[i] == "" {
				return fmt.Errorf("archetype %q declares an empty repository name", name)
			}
		}
	}
	return nil
}

// ArchetypeNames returns the configured archetype names in a stable order, so repeated
// reports of an unchanged topology are byte-identical.
func (t *FleetTopology) ArchetypeNames() []string {
	if t == nil {
		return nil
	}
	names := make([]string, 0, len(t.Archetypes))
	for name := range t.Archetypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TopologyPath reports where the topology is expected, for guidance in reports.
func TopologyPath(rootDir string) string {
	return filepath.Join(rootDir, FleetTopologyFile)
}
