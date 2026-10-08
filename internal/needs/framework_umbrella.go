package needs

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
)

const (
	// maxContractUmbrellas bounds the umbrella packages one contract describes.
	maxContractUmbrellas = 16
	// maxContractGroupings bounds the groupings of one umbrella and the packages of one
	// grouping.
	maxContractGroupings = 64
)

// contractUmbrella describes an umbrella package of a go framework: a package whose only job
// is to group the modules of every subsystem, so that importing it links every subsystem
// into a consumer's build. Each grouping is an exported identifier of the umbrella
// (example.com/kit.HTTP) and lists the declared packages it groups, the imports that replace
// it. Name is the package name the umbrella is imported under when it differs from the
// last element of its import path (hiss.DefaultImportName).
type contractUmbrella struct {
	Import    string             `yaml:"import"`
	Name      string             `yaml:"name,omitempty"`
	Groupings []contractGrouping `yaml:"groupings"`
}

type contractGrouping struct {
	Name     string   `yaml:"name"`
	Packages []string `yaml:"packages"`
}

// FrameworkUmbrella is an umbrella package a framework contract describes: the package
// that groups every subsystem's modules, and the groupings a consumer selects from it.
type FrameworkUmbrella struct {
	ImportPath string `json:"import_path"`
	// Name is the package name the umbrella binds when imported without a rename; empty
	// means the last element of ImportPath without a major-version suffix.
	Name      string             `json:"name,omitempty"`
	Groupings []UmbrellaGrouping `json:"groupings"`
}

// UmbrellaGrouping is one exported grouping of an umbrella and the declared packages it
// groups, in contract order.
type UmbrellaGrouping struct {
	Name     string   `json:"name"`
	Packages []string `json:"packages"`
}

// validateUmbrellas checks a contract's umbrellas against the packages it declares.
// Umbrellas describe Go import paths, so only a go contract may carry them.
func validateUmbrellas(umbrellas []contractUmbrella, ecosystem, framework string, declared map[string]bool) error {
	if len(umbrellas) == 0 {
		return nil
	}
	if ecosystem != contractEcosystemGo {
		return fmt.Errorf("umbrellas describe Go packages; a %s contract cannot carry them", ecosystem)
	}
	if len(umbrellas) > maxContractUmbrellas {
		return fmt.Errorf("umbrellas exceed %d entries", maxContractUmbrellas)
	}
	seen := make(map[string]bool, len(umbrellas))
	for i := range umbrellas {
		if err := umbrellas[i].validate(framework, declared, seen); err != nil {
			return err
		}
	}
	return nil
}

func (u *contractUmbrella) validate(framework string, declared, seen map[string]bool) error {
	if err := u.validateIdentity(framework, seen); err != nil {
		return err
	}
	if len(u.Groupings) == 0 || len(u.Groupings) > maxContractGroupings {
		return fmt.Errorf("umbrella %q needs 1..%d groupings", u.Import, maxContractGroupings)
	}
	names := make(map[string]bool, len(u.Groupings))
	for i := range u.Groupings {
		if err := u.Groupings[i].validate(u.Import, declared, names); err != nil {
			return err
		}
	}
	return nil
}

// validateIdentity checks the umbrella's import path and package name, recording the path in
// seen.
func (u *contractUmbrella) validateIdentity(framework string, seen map[string]bool) error {
	if u.Import != framework && !isNestedModule(u.Import, framework) {
		return fmt.Errorf("umbrella %q is outside %s", u.Import, framework)
	}
	if seen[u.Import] {
		return fmt.Errorf("umbrella %q is described twice", u.Import)
	}
	seen[u.Import] = true
	if u.Name != "" && (u.Name == "_" || !token.IsIdentifier(u.Name)) {
		return fmt.Errorf("umbrella %q declares name %q, which is not a Go package name", u.Import, u.Name)
	}
	return nil
}

func (g *contractGrouping) validate(umbrella string, declared, names map[string]bool) error {
	if !token.IsIdentifier(g.Name) || !token.IsExported(g.Name) {
		return fmt.Errorf("umbrella %q grouping %q is not an exported Go identifier", umbrella, g.Name)
	}
	if names[g.Name] {
		return fmt.Errorf("umbrella %q declares grouping %q twice", umbrella, g.Name)
	}
	names[g.Name] = true
	if len(g.Packages) == 0 || len(g.Packages) > maxContractGroupings {
		return fmt.Errorf("umbrella %q grouping %q needs 1..%d packages", umbrella, g.Name, maxContractGroupings)
	}
	listed := make(map[string]bool, len(g.Packages))
	for _, pkg := range g.Packages {
		if pkg == umbrella || !declared[pkg] || listed[pkg] {
			return fmt.Errorf("umbrella %q grouping %q lists %q, which is the umbrella itself, a repeat or no package the contract declares",
				umbrella, g.Name, pkg)
		}
		listed[pkg] = true
	}
	return nil
}

// indexUmbrellas indexes a contract's umbrellas by import path.
func indexUmbrellas(umbrellas []contractUmbrella) map[string]FrameworkUmbrella {
	if len(umbrellas) == 0 {
		return nil
	}
	out := make(map[string]FrameworkUmbrella, len(umbrellas))
	for _, umbrella := range umbrellas {
		entry := FrameworkUmbrella{ImportPath: umbrella.Import, Name: umbrella.Name,
			Groupings: make([]UmbrellaGrouping, 0, len(umbrella.Groupings))}
		for _, grouping := range umbrella.Groupings {
			entry.Groupings = append(entry.Groupings, UmbrellaGrouping{Name: grouping.Name, Packages: slices.Clone(grouping.Packages)})
		}
		out[umbrella.Import] = entry
	}
	return out
}

// rebaseUmbrellas returns the umbrellas with every import path moved by move, the way
// frameworkContract.rebase moves the packages of a fork.
func rebaseUmbrellas(umbrellas []contractUmbrella, move func(string) string) []contractUmbrella {
	if len(umbrellas) == 0 {
		return nil
	}
	out := make([]contractUmbrella, 0, len(umbrellas))
	for _, umbrella := range umbrellas {
		moved := contractUmbrella{Import: move(umbrella.Import), Name: umbrella.Name,
			Groupings: make([]contractGrouping, 0, len(umbrella.Groupings))}
		for _, grouping := range umbrella.Groupings {
			packages := make([]string, 0, len(grouping.Packages))
			for _, pkg := range grouping.Packages {
				packages = append(packages, move(pkg))
			}
			moved.Groupings = append(moved.Groupings, contractGrouping{Name: grouping.Name, Packages: packages})
		}
		out = append(out, moved)
	}
	return out
}

// contractUmbrellas renders an index's umbrellas for an exported contract, in import order.
// A grouping keeps only the packages the export declares; a grouping left with none, and an
// umbrella left with no grouping, is left out and listed in skipped.
func contractUmbrellas(umbrellas map[string]FrameworkUmbrella, packages []contractPackage, skipped *[]string) []contractUmbrella {
	declared := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		declared[pkg.Import] = true
	}
	var out []contractUmbrella
	for _, path := range slices.Sorted(maps.Keys(umbrellas)) {
		umbrella := umbrellas[path]
		entry := contractUmbrella{Import: path, Name: umbrella.Name}
		for _, grouping := range umbrella.Groupings {
			if kept := exportedGrouping(grouping, declared, path, skipped); len(kept.Packages) > 0 {
				entry.Groupings = append(entry.Groupings, kept)
			}
		}
		if len(entry.Groupings) == 0 {
			*skipped = append(*skipped, fmt.Sprintf("umbrella %s: no grouping lists an exported package", path))
			continue
		}
		out = append(out, entry)
	}
	return out
}

// exportedGrouping returns the grouping with the packages the export does not declare left
// out, each listed in skipped.
func exportedGrouping(grouping UmbrellaGrouping, declared map[string]bool, umbrella string, skipped *[]string) contractGrouping {
	kept := contractGrouping{Name: grouping.Name}
	for _, pkg := range grouping.Packages {
		if !declared[pkg] {
			*skipped = append(*skipped, fmt.Sprintf("package %s of umbrella %s grouping %s: not exported", pkg, umbrella, grouping.Name))
			continue
		}
		kept.Packages = append(kept.Packages, pkg)
	}
	return kept
}
