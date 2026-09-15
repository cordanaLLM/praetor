package flavor

import (
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// builtinFlavorList returns every built-in flavor in detection precedence order, most specific
// first. This slice is the single source of that order.
//
// It used to be stated twice: once here as registration order, and again inside DetectFlavor as a
// hardcoded list of the same eleven names. The two disagreed, and a flavor registered but left
// out of the second list fell through to map iteration order, so its precedence was whatever the
// runtime happened to produce that execution.
//
// The ordering principle is specificity. An agent harness manifest or a Helm chart says what a
// repository is for. A dependency on a machine-learning framework is likewise a positive
// statement, which is why python-ml sits high now that it requires one rather than firing on any
// Python project at all. Language build files come next, and the two Go entries are ordered
// service before library because the service detector is the stricter of the two.
func builtinFlavorList() []Flavor {
	return []Flavor{
		&InfraK8sFlavor{},
		&PythonMLFlavor{},
		&NativeGPUSystemsFlavor{},
		&RustSystemsFlavor{},
		&FrontendSvelteFlavor{},
		&TypeScriptNodeFlavor{},
		&MobileFlutterFlavor{},
		&JVMServiceFlavor{},
		&GoServiceFlavor{},
		&GoLibraryFlavor{},
		// Never auto-detects; listed last so its position does not imply precedence.
		&AgenticAutonomousFlavor{},
	}
}

func builtinFlavors() map[string]Flavor {
	flavors := builtinFlavorList()
	result := make(map[string]Flavor, len(flavors))
	for _, flavor := range flavors {
		result[flavor.Name()] = flavor
	}
	return result
}

// --- 1. Go Service Flavor ---

type GoServiceFlavor struct{}

func (f *GoServiceFlavor) Name() string        { return "go-service" }
func (f *GoServiceFlavor) Description() string { return "Go Microservice, Daemon, or HTTP/gRPC API" }
func (f *GoServiceFlavor) HISSProfile() string { return "framework" }

func (f *GoServiceFlavor) Detect(repoPath string) bool {
	hasMod := CheckFileExists(filepath.Join(repoPath, "go.mod"))
	hasCmd := CheckFileExists(filepath.Join(repoPath, "cmd"))
	hasDocker := CheckFileExists(filepath.Join(repoPath, "Dockerfile")) || CheckFileExists(filepath.Join(repoPath, "docker"))
	return hasMod && (hasCmd || hasDocker)
}

func (f *GoServiceFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: ".golangci.yml", Description: "Unified Go linter configuration"},
		{Path: ".gosec.json", Description: "Go security analyzer configuration"},
		{Path: ".github/workflows/ci.yml", Description: "Continuous integration matrix"},
		{Path: ".github/workflows/security.yml", Description: "Automated vulnerability scan"},
		{Path: "Dockerfile", Description: "Distroless container definition"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *GoServiceFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "Lefthook Git Hooks", Path: "lefthook.yml", Description: "Pre-commit and pre-push gating"},
		{Name: "VSCode Go Settings", Path: ".vscode/settings.json", Description: "Editor workspace settings"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *GoServiceFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "go", Purpose: "Go Compiler & Toolchain", InstallGuide: "https://golang.org/dl/"},
		{Binary: "govulncheck", Purpose: "Go vulnerability detection", InstallGuide: "go install golang.org/x/vuln/cmd/govulncheck@latest"},
		{Binary: "gosec", Purpose: "Go AST security scanner", InstallGuide: "go install github.com/securego/gosec/v2/cmd/gosec@latest"},
		{Binary: "golangci-lint", Purpose: "Go unified linter", InstallGuide: "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run"},
	}
}

// --- 2. Go Library Flavor ---

type GoLibraryFlavor struct{}

func (f *GoLibraryFlavor) Name() string        { return "go-library" }
func (f *GoLibraryFlavor) Description() string { return "Go Core Library, SDK, or Engine" }
func (f *GoLibraryFlavor) HISSProfile() string { return "framework" }

func (f *GoLibraryFlavor) Detect(repoPath string) bool {
	hasMod := CheckFileExists(filepath.Join(repoPath, "go.mod"))
	hasPkg := CheckFileExists(filepath.Join(repoPath, "pkg")) || CheckFileExists(filepath.Join(repoPath, "internal"))
	return hasMod && hasPkg
}

func (f *GoLibraryFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: ".standards.yaml", Description: "Praetor standards declaration"},
		{Path: ".standards.lock", Description: "SemVer lockfile"},
		{Path: ".golangci.yml", Description: "Unified Go linter configuration"},
		{Path: ".github/workflows/ci.yml", Description: "CI cross-platform build matrix"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *GoLibraryFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "Lefthook Git Hooks", Path: "lefthook.yml", Description: "Pre-commit and pre-push gating"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *GoLibraryFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "go", Purpose: "Go Compiler & Toolchain", InstallGuide: "https://golang.org/dl/"},
		{Binary: "govulncheck", Purpose: "Go vulnerability detection", InstallGuide: "go install golang.org/x/vuln/cmd/govulncheck@latest"},
		{Binary: "gosec", Purpose: "Go AST security scanner", InstallGuide: "go install github.com/securego/gosec/v2/cmd/gosec@latest"},
	}
}

// --- 3. Native GPU Systems Flavor ---

type NativeGPUSystemsFlavor struct{}

func (f *NativeGPUSystemsFlavor) Name() string { return "native-gpu-systems" }
func (f *NativeGPUSystemsFlavor) Description() string {
	return "C/C++/CUDA/Vulkan High-Performance Engine"
}
func (f *NativeGPUSystemsFlavor) HISSProfile() string { return "native-gpu-systems" }

func (f *NativeGPUSystemsFlavor) Detect(repoPath string) bool {
	return CheckFileExists(filepath.Join(repoPath, "meson.build")) ||
		CheckFileExists(filepath.Join(repoPath, "core", "meson.build")) ||
		CheckFileExists(filepath.Join(repoPath, "libvmaf", "meson.build")) ||
		CheckFileExists(filepath.Join(repoPath, "CMakeLists.txt"))
}

func (f *NativeGPUSystemsFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: ".clang-tidy", Description: "Clang-tidy AST static analyzer config"},
		{Path: ".clang-format", Description: "C/C++ code formatting rules"},
		{Path: ".gitleaks.toml", Description: "Secret leak detection policy"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *NativeGPUSystemsFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode C/C++ Settings", Path: ".vscode/settings.json", Description: "Editor workspace config"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *NativeGPUSystemsFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "clang-tidy", Purpose: "C/C++ AST analyzer", InstallGuide: "sudo apt-get install clang-tidy"},
		{Binary: "clang-format", Purpose: "C/C++ formatter", InstallGuide: "sudo apt-get install clang-format"},
		{Binary: "meson", Purpose: "Meson build system", InstallGuide: "pip install meson"},
		{Binary: "ninja", Purpose: "Ninja build backend", InstallGuide: "sudo apt-get install ninja-build"},
	}
}

// --- 4. Frontend Svelte Flavor ---

type FrontendSvelteFlavor struct{}

func (f *FrontendSvelteFlavor) Name() string { return "frontend-svelte" }
func (f *FrontendSvelteFlavor) Description() string {
	return "Svelte 5 / TypeScript Modern Web Application"
}
func (f *FrontendSvelteFlavor) HISSProfile() string { return "app-service" }

func (f *FrontendSvelteFlavor) Detect(repoPath string) bool {
	hasPackage := CheckFileExists(filepath.Join(repoPath, "package.json"))
	hasSvelte := CheckFileExists(filepath.Join(repoPath, "svelte.config.js")) ||
		CheckFileExists(filepath.Join(repoPath, "src", "routes")) ||
		CheckFileExists(filepath.Join(repoPath, "vite.config.ts"))
	return hasPackage && hasSvelte
}

func (f *FrontendSvelteFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: "playwright.config.ts", Description: "E2E testing configuration"},
		{Path: ".eslintrc.cjs", Description: "ECMAScript & Svelte linter"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *FrontendSvelteFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode Svelte Settings", Path: ".vscode/settings.json", Description: "Editor workspace config"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *FrontendSvelteFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "pnpm", Purpose: "Fast, disk space efficient package manager", InstallGuide: "npm install -g pnpm"},
		{Binary: "node", Purpose: "Node.js JavaScript runtime", InstallGuide: "https://nodejs.org/"},
	}
}

// --- 5. Python ML Flavor ---

type PythonMLFlavor struct{}

func (f *PythonMLFlavor) Name() string        { return "python-ml" }
func (f *PythonMLFlavor) Description() string { return "Python / PyTorch / OpenVINO ML Pipeline" }
func (f *PythonMLFlavor) HISSProfile() string { return "app-service" }

// Detect requires evidence that this is a machine-learning pipeline, not merely that Python is
// present. The previous predicate fired on any pyproject.toml or requirements.txt, so every
// Python repository in the fleet was reported as a PyTorch/OpenVINO pipeline and was then audited
// against ML tooling it had no reason to install. Measured on cordanaLLM/nucleus, a Linux kernel
// build forge, and cordanaLLM/imago, an OS image forge that is majority Go.
//
// A Python project with no ML dependency now matches nothing here, which is the honest answer:
// no Python library or Python service flavor exists yet, and reporting one that does exist but
// does not fit is worse than reporting none.
func (f *PythonMLFlavor) Detect(repoPath string) bool {
	for _, name := range []string{"pyproject.toml", "requirements.txt"} {
		if declaresMLDependency(filepath.Join(repoPath, name)) {
			return true
		}
	}
	return false
}

// mlDependencyMarkers name the frameworks that make a Python project an ML pipeline. They are
// matched as substrings of the lower-cased dependency declaration, which deliberately accepts
// the family around each name -- torch also matches pytorch-lightning and torchvision, which are
// the same signal -- at the cost of a false positive on any unrelated package whose name happens
// to contain one. No package in common use does.
var mlDependencyMarkers = []string{
	"torch", "tensorflow", "openvino", "scikit-learn", "sklearn",
	"transformers", "onnx", "keras", "lightgbm", "xgboost", "jax",
}

// maxDependencyFileBytes bounds the dependency read (HISS-02).
const maxDependencyFileBytes = 256 * 1024

// declaresMLDependency reports whether a dependency file names a machine-learning framework.
// An unreadable or absent file is not evidence, so it reports false rather than guessing.
func declaresMLDependency(path string) bool {
	// ReadFileNoFollow is reused rather than reimplemented: it already refuses symlinks and
	// non-regular files, which matters here because this reads a path inside a repository the
	// engine does not own. The scan is then bounded independently of the file's size.
	data, err := util.ReadFileNoFollow(path)
	if err != nil {
		return false
	}
	if len(data) > maxDependencyFileBytes {
		data = data[:maxDependencyFileBytes]
	}
	lowered := strings.ToLower(string(data))
	for i := 0; i < len(mlDependencyMarkers) && i < maxMLMarkers; i++ {
		if strings.Contains(lowered, mlDependencyMarkers[i]) {
			return true
		}
	}
	return false
}

// maxMLMarkers bounds the marker scan (HISS-02).
const maxMLMarkers = 32

func (f *PythonMLFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: "ruff.toml", Description: "Fast Python linter and formatter config"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *PythonMLFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode Python Settings", Path: ".vscode/settings.json", Description: "Python interpreter and test runner"},
	}
}

func (f *PythonMLFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "uv", Purpose: "Extremely fast Python package installer and resolver", InstallGuide: "curl -LsSf https://astral.sh/uv/install.sh | sh"},
		{Binary: "ruff", Purpose: "Fast Python linter and code formatter", InstallGuide: "pip install ruff"},
	}
}

// --- 6. Infra K8s Flavor ---

type InfraK8sFlavor struct{}

func (f *InfraK8sFlavor) Name() string        { return "infra-k8s" }
func (f *InfraK8sFlavor) Description() string { return "Kubernetes, GitOps, and Helm Infrastructure" }
func (f *InfraK8sFlavor) HISSProfile() string { return "container-image" }

func (f *InfraK8sFlavor) Detect(repoPath string) bool {
	return CheckFileExists(filepath.Join(repoPath, "Chart.yaml")) ||
		CheckFileExists(filepath.Join(repoPath, "kustomization.yaml")) ||
		CheckFileExists(filepath.Join(repoPath, "helmfile.yaml"))
}

func (f *InfraK8sFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *InfraK8sFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *InfraK8sFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "kubectl", Purpose: "Kubernetes CLI", InstallGuide: "https://kubernetes.io/docs/tasks/tools/"},
		{Binary: "helm", Purpose: "Kubernetes package manager", InstallGuide: "https://helm.sh/docs/intro/install/"},
	}
}

// --- 7. Agentic Autonomous Flavor ---

type AgenticAutonomousFlavor struct{}

func (f *AgenticAutonomousFlavor) Name() string { return "agentic-autonomous" }
func (f *AgenticAutonomousFlavor) Description() string {
	return "Paperclip / Antigravity Agent Runtime Harness"
}
func (f *AgenticAutonomousFlavor) HISSProfile() string { return "framework" }

// Detect never matches. This flavor has to be selected explicitly.
//
// It had two markers and adoption writes both of them. A bare .agents directory is scaffolded
// into every governed repository, and so is .paperclip/harness.json, so every adopted repository
// looked like an agent runtime harness. That was masked only because this flavor sat last in a
// hardcoded precedence list that disagreed with the registry; ordering the two consistently
// exposed it immediately, and cordanaLLM/imago -- an OS image forge -- audited as an agent
// runtime at 100%.
//
// A flavor whose only evidence is produced by the governance tool itself cannot be detected from
// that evidence. Reporting so is the honest contract: `--flavor=agentic-autonomous` still works,
// and the repository is left unmatched rather than confidently misdescribed.
func (f *AgenticAutonomousFlavor) Detect(_ string) bool {
	return false
}

func (f *AgenticAutonomousFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: ".paperclip/harness.json", Description: "Agent runtime operating contract and invariants"},
		{Path: "AGENTS.md", Description: "Canonical agent operating harness"},
		{Path: "CLAUDE.md", Description: "Compiled Claude instructions"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *AgenticAutonomousFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "Lefthook Git Hooks", Path: "lefthook.yml", Description: "Anti-evasion and verification hooks"},
	}
}

func (f *AgenticAutonomousFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "praetorctl", Purpose: "Universal Fleet Governance CLI", InstallGuide: "make build && cp bin/praetorctl ~/.local/bin/"},
		{Binary: "git", Purpose: "Distributed version control system", InstallGuide: "sudo apt-get install git"},
	}
}

// --- 8. Rust Systems Flavor ---

type RustSystemsFlavor struct{}

func (f *RustSystemsFlavor) Name() string        { return "rust-systems" }
func (f *RustSystemsFlavor) Description() string { return "Rust CLI, Systems Engine, or Daemon" }
func (f *RustSystemsFlavor) HISSProfile() string { return "native-gpu-systems" }

func (f *RustSystemsFlavor) Detect(repoPath string) bool {
	return CheckFileExists(filepath.Join(repoPath, "Cargo.toml"))
}

func (f *RustSystemsFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: "rustfmt.toml", Description: "Rust formatting and style guidelines"},
		{Path: "clippy.toml", Description: "Rust AST and idiomatic static linting configuration"},
		{Path: ".github/workflows/ci.yml", Description: "Continuous integration cargo build, test, and clippy"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *RustSystemsFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "Lefthook Git Hooks", Path: "lefthook.yml", Description: "Pre-commit clippy and rustfmt enforcement"},
		{Name: "VSCode Rust Settings", Path: ".vscode/settings.json", Description: "Rust-analyzer and clippy editor configuration"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *RustSystemsFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "cargo", Purpose: "Rust package manager & compiler frontend", InstallGuide: "https://rustup.rs/"},
		{Binary: "rustc", Purpose: "Rust compiler", InstallGuide: "https://rustup.rs/"},
		{Binary: "cargo-clippy", Purpose: "Rust compiler linting harness", InstallGuide: "rustup component add clippy"},
		{Binary: "cargo-audit", Purpose: "Rust security vulnerability scanner", InstallGuide: "cargo install cargo-audit"},
	}
}

// --- 9. TypeScript Node Flavor ---

type TypeScriptNodeFlavor struct{}

func (f *TypeScriptNodeFlavor) Name() string { return "typescript-node" }
func (f *TypeScriptNodeFlavor) Description() string {
	return "Node.js / TypeScript Backend API, CLI, or Service"
}
func (f *TypeScriptNodeFlavor) HISSProfile() string { return "app-service" }

func (f *TypeScriptNodeFlavor) Detect(repoPath string) bool {
	hasPackage := CheckFileExists(filepath.Join(repoPath, "package.json"))
	hasSvelte := CheckFileExists(filepath.Join(repoPath, "svelte.config.js")) ||
		CheckFileExists(filepath.Join(repoPath, "src", "routes"))
	return hasPackage && !hasSvelte
}

func (f *TypeScriptNodeFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{
			Path:        "tsconfig.json",
			Description: "TypeScript compiler options and strict type checking",
			AltPaths:    []string{"tsconfig.base.json", "jsconfig.json"},
		},
		{
			Path:        ".eslintrc.json",
			Description: "TypeScript / Node.js static analysis rules",
			AltPaths: []string{
				"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
				"eslint.config.ts", "eslint.config.mts", ".eslintrc.cjs", ".eslintrc.js",
			},
		},
		{Path: ".github/workflows/ci.yml", Description: "Node.js CI test and build matrix"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *TypeScriptNodeFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode TypeScript Settings", Path: ".vscode/settings.json", Description: "TypeScript language server and formatter configuration"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *TypeScriptNodeFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "node", Purpose: "Node.js JavaScript runtime", InstallGuide: "https://nodejs.org/"},
		{
			Binary:       "npm",
			Purpose:      "Node package manager",
			InstallGuide: "https://nodejs.org/",
			AltBinaries:  []string{"pnpm", "yarn", "bun"},
		},
		{
			Binary:       "tsc",
			Purpose:      "TypeScript compiler",
			InstallGuide: "npm install -g typescript",
			ProjectLocal: true,
		},
	}
}

// --- 10. JVM Service Flavor ---

type JVMServiceFlavor struct{}

func (f *JVMServiceFlavor) Name() string        { return "jvm-service" }
func (f *JVMServiceFlavor) Description() string { return "Java / Kotlin Maven or Gradle Microservice" }
func (f *JVMServiceFlavor) HISSProfile() string { return "app-service" }

func (f *JVMServiceFlavor) Detect(repoPath string) bool {
	return CheckFileExists(filepath.Join(repoPath, "pom.xml")) ||
		CheckFileExists(filepath.Join(repoPath, "build.gradle")) ||
		CheckFileExists(filepath.Join(repoPath, "build.gradle.kts")) ||
		CheckFileExists(filepath.Join(repoPath, "mvnw")) ||
		CheckFileExists(filepath.Join(repoPath, "gradlew"))
}

func (f *JVMServiceFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: "checkstyle.xml", Description: "JVM code style and static analysis rules"},
		{Path: ".github/workflows/ci.yml", Description: "Java / Kotlin build, test, and verification matrix"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *JVMServiceFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode Java Settings", Path: ".vscode/settings.json", Description: "Java language server and build tool config"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *JVMServiceFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "java", Purpose: "Java Virtual Machine & Development Kit", InstallGuide: "https://adoptium.net/"},
		{Binary: "mvn", Purpose: "Apache Maven build tool", InstallGuide: "sudo apt-get install maven"},
	}
}

// --- 11. Mobile Flutter Flavor ---

type MobileFlutterFlavor struct{}

func (f *MobileFlutterFlavor) Name() string { return "mobile-flutter" }
func (f *MobileFlutterFlavor) Description() string {
	return "Flutter / Dart Multiplatform Mobile and Desktop Application"
}
func (f *MobileFlutterFlavor) HISSProfile() string { return "app-service" }

func (f *MobileFlutterFlavor) Detect(repoPath string) bool {
	return CheckFileExists(filepath.Join(repoPath, "pubspec.yaml"))
}

func (f *MobileFlutterFlavor) RequiredTemplates() []TemplateItem {
	return []TemplateItem{
		{Path: "analysis_options.yaml", Description: "Dart and Flutter analyzer linter configuration"},
		{Path: ".github/workflows/ci.yml", Description: "Flutter test and build validation matrix"},
		{Path: ".workingdir/STATE.md", Description: "Session state ledger"},
		{Path: ".workingdir/BUGS.md", Description: "Bug discovery ledger"},
		{Path: ".workingdir/QUESTIONS.md", Description: "User decisions collection"},
	}
}

func (f *MobileFlutterFlavor) RequiredSettings() []SettingItem {
	return []SettingItem{
		{Name: "VSCode Dart Settings", Path: ".vscode/settings.json", Description: "Dart and Flutter editor workspace configuration"},
		{Name: "Branch Protection Ruleset", Path: ".github/rulesets/main.json", Description: "Main branch merge restrictions"},
	}
}

func (f *MobileFlutterFlavor) RequiredToolchains() []ToolchainItem {
	return []ToolchainItem{
		{Binary: "flutter", Purpose: "Flutter SDK & build tool", InstallGuide: "https://docs.flutter.dev/get-started/install"},
		{Binary: "dart", Purpose: "Dart language SDK", InstallGuide: "https://dart.dev/get-dart"},
	}
}
