// Package classify decides what a repository is, once.
//
// Four places used to answer that question independently and disagree. Measured on one copy of
// cordanaLLM/imago on 2026-09-15, the same repository was python-ml by flavor markers,
// app-service by description keywords, framework by adoption markers, and gitops-infra by its
// own declaration -- four answers, no arbitration, and nothing reading the declaration.
//
// The divergence was not a difference of opinion. Where two marker tables both had a rule they
// agreed on all nine overlapping markers; every disagreement came from one table lacking a rule
// the other had, or from a default that looked like a match. So this is one table, written once,
// with the union of the rules and a result that can say it matched nothing.
package classify

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Source records what decided a classification. It is part of the result because "the repository
// says so" and "a file called go.mod exists" are not the same claim, and a caller that cannot
// tell them apart will overwrite the first with the second.
type Source string

const (
	// SourceDeclared is the repository's own .standards.yaml. It outranks every guess.
	SourceDeclared Source = "declared"
	// SourceExplicit is an operator-supplied profile, such as an --profile flag.
	SourceExplicit Source = "explicit"
	// SourceMarkers is a file present in the working tree.
	SourceMarkers Source = "markers"
	// SourceMetadata is remote repository language and description text, used where no working
	// tree is available. It is the weakest evidence: it reads prose.
	SourceMetadata Source = "metadata"
	// SourceNone means nothing matched. It is a representable answer rather than a default,
	// because "no profile fits this repository" is information the caller needs.
	SourceNone Source = "none"
)

// Result is one classification and the evidence behind it.
type Result struct {
	Archetype string
	Source    Source
}

// Matched reports whether anything actually decided this result.
func (r Result) Matched() bool {
	return r.Source != SourceNone && r.Archetype != ""
}

// FallbackArchetype is what callers that must name something use when nothing matched. It is
// exported so the substitution is visible at the call site rather than buried in a detector.
const FallbackArchetype = "template-seed"

// Or returns the archetype, or the given replacement when nothing matched.
func (r Result) Or(replacement string) string {
	if r.Matched() {
		return r.Archetype
	}
	return replacement
}

// maxRules bounds the table scan (HISS-02).
const maxRules = 32

// maxMarkers bounds one rule's marker scan (HISS-02).
const maxMarkers = 16

// rule maps the presence of any one marker to an archetype.
type rule struct {
	markers   []string
	archetype string
}

// rules is the single ordered marker table, most specific first.
//
// The order is the one thing two tables genuinely disagreed about, so it is stated here with its
// reasoning rather than left implicit. A repository holding several markers is classified by the
// most specific one present: an agent harness or a Helm chart says what the repository is *for*,
// while a Dockerfile says only that it ships in a container, which is true of nearly everything.
// Dockerfile is therefore last, exactly where the adoption table already had it.
func rules() []rule {
	return []rule{
		{[]string{"harness.json"}, "framework"},
		// An image forge is known by what it builds, and its Go, Python and shell content is
		// the tooling that builds it. Ahead of go.mod for that reason: cordanaLLM/imago has a
		// go.mod for its CLI and was classified framework by it, which described the tool
		// rather than the product.
		{[]string{"packer/*.pkr.hcl", "mkosi.conf", "build/mkosi.conf"}, "os-image"},
		{[]string{"Chart.yaml", "kustomization.yaml", "helmfile.yaml"}, "container-image"},
		{[]string{"meson.build", "core/meson.build", "libvmaf/meson.build", "CMakeLists.txt"}, "native-gpu-systems"},
		{[]string{"Cargo.toml"}, "native-gpu-systems"},
		{[]string{"go.mod"}, "framework"},
		{[]string{"pubspec.yaml", "pom.xml", "build.gradle", "build.gradle.kts"}, "app-service"},
		{[]string{"package.json"}, "app-service"},
		{[]string{"pyproject.toml", "requirements.txt"}, "app-service"},
		{[]string{"Dockerfile"}, "container-image"},
	}
}

// ByMarkers classifies a working tree. An empty repoPath matches nothing rather than probing the
// process working directory, which would classify whatever happened to be current.
func ByMarkers(repoPath string) Result {
	if strings.TrimSpace(repoPath) == "" {
		return Result{Source: SourceNone}
	}
	table := rules()
	for i := 0; i < len(table) && i < maxRules; i++ {
		if ruleMatches(repoPath, table[i]) {
			return Result{Archetype: table[i].archetype, Source: SourceMarkers}
		}
	}
	return Result{Source: SourceNone}
}

// ruleMatches reports whether any of the rule's markers is present.
func ruleMatches(repoPath string, r rule) bool {
	for i := 0; i < len(r.markers) && i < maxMarkers; i++ {
		if util.MarkerExists(repoPath, r.markers[i]) {
			return true
		}
	}
	return false
}

// ArchetypeFor returns the archetype a marker implies, so other packages can check their own
// detection against this table instead of restating it.
func ArchetypeFor(marker string) (string, bool) {
	table := rules()
	for i := 0; i < len(table) && i < maxRules; i++ {
		for j := 0; j < len(table[i].markers) && j < maxMarkers; j++ {
			if table[i].markers[j] == marker {
				return table[i].archetype, true
			}
		}
	}
	return "", false
}

// FromDeclaration classifies from a repository's declared profiles. The first declared profile is
// the primary one; additional profiles layer on top of it and do not change what the repository
// fundamentally is.
func FromDeclaration(profiles []string) Result {
	for i := 0; i < len(profiles) && i < maxRules; i++ {
		if declared := strings.TrimSpace(profiles[i]); declared != "" {
			return Result{Archetype: declared, Source: SourceDeclared}
		}
	}
	return Result{Source: SourceNone}
}

// Explicit classifies from an operator-supplied profile.
func Explicit(profile string) Result {
	if trimmed := strings.TrimSpace(profile); trimmed != "" {
		return Result{Archetype: trimmed, Source: SourceExplicit}
	}
	return Result{Source: SourceNone}
}

// metadataRule maps remote repository metadata to an archetype.
type metadataRule struct {
	keywords  []string
	languages []string
	archetype string
}

// metadataRules classifies where there is no working tree to inspect, from a repository's
// language and description. It is deliberately the weakest evidence in the precedence chain.
//
// The word "kernel" is absent on purpose. It used to map to native-gpu-systems as a bare
// substring, which classified cordanaLLM/nucleus -- a Linux kernel build forge -- as a
// GPU compute engine. A genuine GPU repository still matches on gpu, vulkan, cuda or sycl, so
// the ambiguous term bought nothing and cost a wrong answer.
func metadataRules() []metadataRule {
	return []metadataRule{
		{keywords: []string{"gpu", "vulkan", "ffmpeg", "sycl", "cuda"}, archetype: "native-gpu-systems"},
		{keywords: []string{"kubernetes", "argocd", "terraform", "gitops", "helm"}, archetype: "gitops-infra"},
		{keywords: []string{"framework", "composable"}, archetype: "framework"},
		{keywords: []string{"client", "sdk", "modules"}, archetype: "library-client"},
		{keywords: []string{"static", "pages"}, languages: []string{"astro"}, archetype: "pages-site"},
		{languages: []string{"rust", "go", "python", "typescript", "dart", "flutter", "java", "kotlin"}, archetype: "app-service"},
	}
}

// ByMetadata classifies from a repository's language and description.
func ByMetadata(language, description string) Result {
	words := descriptionWords(description)
	lowerLang := strings.ToLower(strings.TrimSpace(language))
	table := metadataRules()
	for i := 0; i < len(table) && i < maxRules; i++ {
		if metadataMatches(table[i], words, lowerLang) {
			return Result{Archetype: table[i].archetype, Source: SourceMetadata}
		}
	}
	return Result{Source: SourceNone}
}

// metadataMatches reports whether one rule applies to the words and language.
func metadataMatches(r metadataRule, words map[string]struct{}, lowerLang string) bool {
	for i := 0; i < len(r.keywords) && i < maxMarkers; i++ {
		if _, ok := words[r.keywords[i]]; ok {
			return true
		}
	}
	for i := 0; i < len(r.languages) && i < maxMarkers; i++ {
		if lowerLang == r.languages[i] {
			return true
		}
	}
	return false
}

// maxDescriptionWords bounds the description scan (HISS-02).
const maxDescriptionWords = 512

// descriptionWords splits a description into lower-cased words.
//
// Whole words, not substrings. Substring matching made every rule fire on any longer word that
// happened to contain it, which is how a description mentioning the Linux kernel matched a GPU
// rule. Splitting on anything that is not a letter or digit also means hyphenated and punctuated
// text is matched the way a reader would read it.
func descriptionWords(description string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(description), func(r rune) bool {
		return !isWordRune(r)
	})
	words := make(map[string]struct{}, len(fields))
	for i := 0; i < len(fields) && i < maxDescriptionWords; i++ {
		words[fields[i]] = struct{}{}
	}
	return words
}

// isWordRune reports whether r can appear inside a matched word.
func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// Resolve applies the precedence the engine was missing: what the repository declares outranks
// what an operator passes, which outranks what the working tree looks like, which outranks what
// the description says. The first matched candidate wins; if none match, the result says so.
func Resolve(candidates ...Result) Result {
	for i := 0; i < len(candidates) && i < maxRules; i++ {
		if candidates[i].Matched() {
			return candidates[i]
		}
	}
	return Result{Source: SourceNone}
}
