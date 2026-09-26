package flavor

// eslintConfigPath is the ESLint configuration a flavor scaffolds when a repository has none.
//
// It is flat config because flat config is what ESLint loads: it has been the default since v9.0.0,
// and the configuration shipped catalog already refuses to name .eslintrc.* on the grounds that
// ESLint v10 "no longer supports" that format (internal/config/shipped_catalog_test.go). The
// flavors kept scaffolding .eslintrc.json and .eslintrc.cjs because that guard reads catalog
// profiles and never read flavor definitions (#137, BUG-953).
//
// The extension is .mjs rather than .js on purpose. A .js file is ESM or CommonJS depending on the
// repository's package.json "type", so a scaffolded eslint.config.js written as ESM fails to load in
// any CommonJS repository. .mjs is ESM unconditionally.
const eslintConfigPath = "eslint.config.mjs"

// eslintConfigAlternatives lists every file name that already configures ESLint, so a repository
// carrying any of them is conforming and no second, contradictory config is scaffolded beside it.
//
// The flat-config names follow ESLint's own search order. Legacy eslintrc names are accepted here
// -- a repository still on ESLint 8 is configured -- but never scaffolded or required: accepting an
// existing file does not ask anyone to create one.
var eslintConfigAlternatives = []string{
	"eslint.config.js", "eslint.config.cjs",
	"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
	".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.json", ".eslintrc",
}

// eslintConfigSource is the scaffolded body: ESLint's recommended rules for JavaScript, in the
// form @eslint/js documents (templates/node/eslint.config.mjs.tmpl).
const eslintConfigSource = "node/eslint.config.mjs.tmpl"

// eslintTemplate is the ESLint requirement both JavaScript flavors declare, so the path, its
// alternatives, its body and its validator are stated once. carriesCode is the validator
// because the alternatives span JavaScript, JSON and YAML: what holds for all of them is
// that a comment-only placeholder configures nothing.
func eslintTemplate(description string) TemplateItem {
	return TemplateItem{
		Path:        eslintConfigPath,
		Description: description,
		Source:      eslintConfigSource,
		AltPaths:    eslintConfigAlternatives,
		Validator:   carriesCode,
	}
}
