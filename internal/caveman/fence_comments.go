package caveman

import "github.com/cordanaLLM/praetor/internal/util"

// Comment syntaxes shared by several fence languages.
var (
	slashComments  = util.CommentSyntax{Line: []string{"//"}, Block: [][2]string{{"/*", "*/"}}}
	hashComments   = util.CommentSyntax{Line: []string{"#"}}
	markupComments = util.CommentSyntax{Block: [][2]string{{"<!--", "-->"}}}
)

// commentGroups lists the comment syntax of the source and data fence languages, by their
// GitHub Linguist names and common aliases, so a corrected comment loses no fact (#322). A
// language missing here, such as json or text, has no comments: all of a line is code.
var commentGroups = []struct {
	syntax util.CommentSyntax
	langs  []string
}{
	{slashComments, []string{"go", "golang", "c", "h", "cpp", "c++", "cc", "hpp", "csharp", "cs",
		"c#", "java", "javascript", "js", "mjs", "cjs", "jsx", "typescript", "ts", "tsx", "rust",
		"rs", "kotlin", "kt", "kts", "swift", "scala", "groovy", "gradle", "dart", "proto",
		"protobuf", "jsonc", "json5", "scss", "less", "objc", "objective-c", "zig"}},
	{util.CommentSyntax{Block: [][2]string{{"/*", "*/"}}}, []string{"css"}},
	{hashComments, []string{"yaml", "yml", "toml", "python", "py", "ruby", "rb", "perl", "pl",
		"r", "dockerfile", "docker", "containerfile", "make", "makefile", "mk", "cmake", "nix",
		"elixir", "ex", "exs", "conf", "gitignore", "dotenv", "env", "tcl", "awk", "graphql",
		"gql", "nginx", "starlark", "bzl", "julia", "jl", "coffee", "coffeescript", "requirements"}},
	{util.CommentSyntax{Line: []string{"#", "//"}, Block: [][2]string{{"/*", "*/"}}},
		[]string{"hcl", "terraform", "tf", "php"}},
	{util.CommentSyntax{Line: []string{";", "#"}}, []string{"ini", "cfg", "editorconfig", "gitconfig"}},
	{util.CommentSyntax{Line: []string{"#", "!"}}, []string{"properties", "java-properties"}},
	{util.CommentSyntax{Line: []string{"--"}, Block: [][2]string{{"/*", "*/"}}},
		[]string{"sql", "mysql", "postgresql", "postgres", "psql", "plsql", "sqlite"}},
	{util.CommentSyntax{Line: []string{"--"}, Block: [][2]string{{"--[[", "]]"}}}, []string{"lua"}},
	{util.CommentSyntax{Line: []string{"--"}, Block: [][2]string{{"{-", "-}"}}},
		[]string{"haskell", "hs", "elm", "purescript"}},
	{markupComments, []string{"html", "xhtml", "htm", "xml", "svg", "xsd", "rss", "wsdl", "plist",
		"vue", "svelte", "markdown", "md"}},
	{util.CommentSyntax{Line: []string{"%"}}, []string{"tex", "latex", "erlang", "erl", "matlab", "octave"}},
	{util.CommentSyntax{Line: []string{";"}}, []string{"lisp", "clojure", "clj", "scheme", "elisp",
		"emacs-lisp", "racket", "asm", "nasm"}},
	{util.CommentSyntax{Line: []string{"%%"}}, []string{"mermaid"}},
}

// sourceComments maps each fence language of commentGroups to its comment syntax.
var sourceComments = commentTable()

func commentTable() map[string]util.CommentSyntax {
	table := map[string]util.CommentSyntax{}
	for _, group := range commentGroups {
		for _, lang := range group.langs {
			table[lang] = group.syntax
		}
	}
	return table
}

// commentSyntax returns the comment syntax of a fenced line: its shell's for a shell fence
// (util.MarkdownShellComments), a script's for a fence without a language, which reads as a
// script, and its language's (sourceComments) for every other fence.
func commentSyntax(ln line) util.CommentSyntax {
	switch {
	case ln.shell != util.ShellNone:
		return util.MarkdownShellComments(ln.shell)
	case ln.lang == "":
		return util.MarkdownShellComments(util.ShellScript)
	}
	return sourceComments[ln.lang]
}
