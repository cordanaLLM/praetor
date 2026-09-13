package adopt

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func addNodeVerification(p *VerificationPlan, inputs verificationInputs) error {
	data, ok := inputs.files["package.json"]
	if !ok {
		return nil
	}
	p.Runtimes = append(p.Runtimes, "node")
	fields, err := verificationObject(data, "scripts", "packageManager")
	if err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	manager, err := verificationString(fields["packageManager"])
	if err != nil {
		return err
	}
	if manager != "" && !strings.HasPrefix(manager, "npm@") {
		p.unavailable("Select commands for the declared package manager; npm cannot stand in for another package manager.")
		return nil
	}
	if fields["scripts"] == nil {
		p.unavailable("package.json has no declared scripts; define and exercise a test script.")
		return nil
	}
	scripts, err := verificationObject(fields["scripts"], "test", "build", "check")
	if err != nil {
		return fmt.Errorf("package.json scripts: %w", err)
	}
	for _, name := range []string{"build", "check", "test"} {
		value, err := verificationString(scripts[name])
		if err != nil {
			return err
		}
		addNodeScript(p, name, strings.TrimSpace(value) != "")
	}
	return nil
}

func addNodeScript(p *VerificationPlan, name string, exists bool) {
	if !exists {
		if name == "test" {
			p.unavailable("package.json has no nonempty test script; a missing script is not a passing gate.")
		}
		return
	}
	command := []string{"npm", "run", name}
	if name == "build" {
		p.Build = append(p.Build, command)
	} else {
		p.Test = append(p.Test, command)
	}
}

func addPythonVerification(p *VerificationPlan, inputs verificationInputs) {
	if !inputs.has("pyproject.toml") && !inputs.has("pytest.ini") && !inputs.has(".pytest.ini") && len(inputs.pythonDirectories) == 0 {
		return
	}
	p.Runtimes = append(p.Runtimes, "python")
	if inputs.has("pytest.ini") || inputs.has(".pytest.ini") {
		p.Test = append(p.Test, []string{"python3", "-m", "pytest"})
		return
	}
	if len(inputs.pythonDirectories) > 0 && pinnedModernPython(inputs.files[".python-version"]) {
		// Python 3.14's unittest CLI fails with exit 5 when discovery runs no tests.
		// The runtime check prevents older Python from silently accepting that case.
		p.Test = append(p.Test, []string{"python3", "-c", "import sys; sys.exit(0 if sys.version_info >= (3, 14) else 'Python 3.14+ is required for zero-test failure detection')"})
		dirs := make([]string, 0, len(inputs.pythonDirectories))
		for dir := range inputs.pythonDirectories {
			dirs = append(dirs, dir)
		}
		slices.Sort(dirs)
		for _, dir := range dirs {
			p.Test = append(p.Test, []string{"python3", "-m", "unittest", "discover", "-s", dir})
		}
		return
	}
	p.unavailable("Select the Python test runner explicitly: pytest.ini, or tests/ with a Python 3.14+ pin; pyproject.toml alone does not identify a runnable test gate.")
}

func pinnedModernPython(data []byte) bool {
	parts := strings.Split(strings.TrimSpace(string(data)), ".")
	if len(parts) != 3 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	return majorErr == nil && minorErr == nil && patchErr == nil && major == 3 && minor >= 14 && patch >= 0
}
