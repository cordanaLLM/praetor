package adopt

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
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
	if declared, _, ok := nodemanifest.DeclaredManager(manager); ok && declared != "" {
		p.UIPackageManager = string(declared)
	}
	if !nodemanifest.NpmRuns(manager) {
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
	return addNodeScripts(p, scripts)
}

// addNodeScripts adds the gate scripts that can pass, leaving out a test-recipe script that only
// runs the other one (nodeScriptRunsOnly) and naming it in the report.
func addNodeScripts(p *VerificationPlan, scripts map[string]json.RawMessage) error {
	bodies := map[string]string{}
	for _, name := range nodeGateScripts {
		value, err := verificationString(scripts[name])
		if err != nil {
			return err
		}
		if nodemanifest.ScriptRuns(value) {
			bodies[name] = value
		}
	}
	for _, name := range nodeGateScripts {
		if target, alias := nodeScriptRunsOnly(name, bodies); alias {
			p.SelectedBy = append(p.SelectedBy, fmt.Sprintf("npm run %s left out: package.json scripts.%s only runs scripts.%s (%s)",
				name, name, target, strings.TrimSpace(bodies[name])))
			continue
		}
		_, exists := bodies[name]
		addNodeScript(p, name, exists)
	}
	return nil
}

// nodeGateScripts are the package.json scripts the generated recipes run, in recipe order: build
// in the build recipe, check and test in the test recipe.
var nodeGateScripts = []string{"build", "check", "test"}

// nodeTestScripts are the gate scripts of the test recipe.
var nodeTestScripts = []string{"check", "test"}

// nodeScriptRunsOnly reports whether the test-recipe script name does nothing but run the other
// test-recipe script, and names it (#1264). With "check": "npm test" the test recipe would run
// the suite twice, once through each script; the script it ends at runs once. Only check and test
// are read: dropping build, or a script that only runs build, could leave a recipe empty and turn
// a declared plan into an unavailable one. Two scripts that alias each other run no work of their
// own, and both are kept, so nothing that ran before is dropped.
func nodeScriptRunsOnly(name string, bodies map[string]string) (string, bool) {
	if !slices.Contains(nodeTestScripts, name) {
		return "", false
	}
	target, alias := nodeScriptAliasTarget(bodies[name])
	if !alias || target == name || !slices.Contains(nodeTestScripts, target) {
		return "", false
	}
	if _, exists := bodies[target]; !exists {
		return "", false
	}
	if back, cycle := nodeScriptAliasTarget(bodies[target]); cycle && back == name {
		return "", false
	}
	return target, true
}

// nodeScriptAliasTarget names the script a package.json script body runs when the body is
// exactly one of the alias forms below and nothing else: `npm test`, `npm t`, `npm tst`,
// `pnpm test`, `pnpm t` and `yarn test` run the test script; `npm run <s>`, `npm run-script <s>`,
// `pnpm run <s>` and `yarn run <s>` run script <s>. The body is read with the engine's shell
// reader (forge.SoleCommand), so a body with more than one command, an assignment, a wrapper or
// any further argument is no alias. Other forms (`yarn <s>`, `pnpm <s>`) are not read: a
// manager's own command of the same name, such as yarn check, would shadow the script.
func nodeScriptAliasTarget(body string) (string, bool) {
	program, args, ok := forge.SoleCommand(body)
	if !ok {
		return "", false
	}
	testForms := map[string][]string{"npm": {"test", "t", "tst"}, "pnpm": {"test", "t"}, "yarn": {"test"}}
	runForms := map[string][]string{"npm": {"run", "run-script"}, "pnpm": {"run"}, "yarn": {"run"}}
	switch {
	case len(args) == 1 && slices.Contains(testForms[program], args[0]):
		return "test", true
	case len(args) == 2 && slices.Contains(runForms[program], args[0]):
		return args[1], true
	}
	return "", false
}

func addNodeScript(p *VerificationPlan, name string, exists bool) {
	if !exists {
		if name == "test" {
			p.unavailable("package.json has no test script that can pass (it is missing, blank, or the placeholder npm init writes); a missing script is not a passing gate.")
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

func addPythonVerification(p *VerificationPlan, inputs verificationInputs) error {
	config, pytest, err := pytestConfiguration(inputs)
	if err != nil {
		return err
	}
	if !inputs.has("pyproject.toml") && !pytest && len(inputs.pythonDirectories) == 0 {
		return nil
	}
	p.Runtimes = append(p.Runtimes, "python")
	if pytest {
		command := []string{"python3", "-m", "pytest"}
		p.Test = append(p.Test, command)
		p.SelectedBy = append(p.SelectedBy, verificationCommand(command)+" from the pytest configuration in "+config)
		return nil
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
		return nil
	}
	p.unavailable("Select the Python test runner explicitly: a pytest configuration (pytest.toml, pytest.ini, a [tool.pytest] or [tool.pytest.ini_options] table in pyproject.toml, a [pytest] section in tox.ini or a [tool:pytest] section in setup.cfg), or tests/ with a Python 3.14+ pin; a pyproject.toml without a pytest table does not identify a runnable test gate.")
	return nil
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
