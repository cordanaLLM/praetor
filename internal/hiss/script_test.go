// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strconv"
	"strings"
	"testing"
)

// scriptFuncOfLOC returns a JavaScript function declaration spanning loc lines, header and
// closing brace included.
func scriptFuncOfLOC(name string, loc int) string {
	var sb strings.Builder
	sb.WriteString("function " + name + "() {\n")
	for i := 0; i < loc-2; i++ {
		sb.WriteString("  work();\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// assertScriptFindings scans one script file and compares its findings, as rule@line, with want.
func assertScriptFindings(t *testing.T, name, body string, want ...string) *ScanReport {
	t.Helper()
	rep := scanFixtureFile(t, name, body)
	got := make([]string, 0, len(rep.Violations))
	for _, v := range rep.Violations {
		got = append(got, v.RuleID+"@"+strconv.Itoa(v.LineNumber))
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s: findings %v, want %v; violations %+v", name, got, want, rep.Violations)
	}
	return rep
}

// Positive (#589): every invariant the script scanner claims is reported in JavaScript,
// TypeScript and a Svelte component's script block. Before, each of these files was counted as
// unscanned and the audit printed a clean PASS over them.
func TestScriptScanner_ReportsEachInvariant(t *testing.T) {
	assertScriptFindings(t, "probe.js",
		"function walk(n) {\n  return n ? walk(n - 1) : 0;\n}\n", "HISS-01@2")
	assertScriptFindings(t, "loops.ts",
		"export function spin(): void {\n  while (true) { tick(); }\n  for ( ; ; ) { tick(); }\n  do { tick(); } while ( 1 );\n}\n",
		"HISS-02@2", "HISS-02@3", "HISS-02@4")
	assertScriptFindings(t, "long.mjs", scriptFuncOfLOC("long", 61), "HISS-04@1")
	assertScriptFindings(t, "errors.js",
		"function load() {\n  try { run(); } catch (e) {}\n  try { run(); } catch {\n  }\n  job().catch(() => {});\n  process.exit(1);\n}\n",
		"HISS-07@2", "HISS-07@3", "HISS-07@5", "HISS-07@6")
	assertScriptFindings(t, "dynamic.cjs",
		"const run = (code) => {\n  eval(code);\n  const f = new Function(code);\n  setTimeout(\"tick()\", 10);\n  return f;\n};\n",
		"HISS-08@2", "HISS-08@3", "HISS-08@4")
	rep := assertScriptFindings(t, "Probe.svelte",
		"<h1>{title}</h1>\n<script lang=\"ts\">\n  export let title: string;\n  eval(title);\n</script>\n<p>{eval(title)}</p>\n",
		"HISS-08@4")
	if rep.Coverage.FilesRead != 1 || rep.Coverage.LanguagesRead["svelte"] != 1 {
		t.Errorf("a Svelte component must be read as svelte, got %+v", rep.Coverage)
	}
}

// Positive: each spelling that reaches a function from its own body is reported: a bound arrow,
// a named function expression under either name, a method and a class field through this.
func TestScriptScanner_RecursionSpellings(t *testing.T) {
	assertScriptFindings(t, "arrow.ts", "export const fact = (n: number): number => {\n  return n <= 1 ? 1 : n * fact(n - 1);\n};\n", "HISS-01@2")
	assertScriptFindings(t, "expr.js", "const outer = function inner(n) {\n  if (n) { inner(n - 1); outer(n - 1); }\n};\n", "HISS-01@2")
	assertScriptFindings(t, "method.ts", "class Tree {\n  depth(node: Node): number {\n    return 1 + this.depth(node.child);\n  }\n  handle = async (e: Event) => {\n    await this.handle(e);\n  };\n}\n",
		"HISS-01@3", "HISS-01@6")
	assertScriptFindings(t, "wrapped.ts", "export function visit(\n  node: Node,\n  depth: number,\n): void {\n  visit(node.next, depth + 1);\n}\n", "HISS-01@5")
	assertScriptFindings(t, "oneline.js", "const f = (n) => { return n ? f(n - 1) : 0; };\n", "HISS-01@1")
}

// Negative: names that do not reach the function are not recursion. A bare call inside a
// method reaches a module function, a call through another object is delegation, and a local
// binding of the name (parameter, declarator, nested function, arrow or catch parameter)
// captures the bare call.
func TestScriptScanner_RecursionNegatives(t *testing.T) {
	for name, body := range map[string]string{
		"method-bare.js":   "class A {\n  run() {\n    return run();\n  }\n}\n",
		"delegation.js":    "function load(x) {\n  return cache.load(x) ?? $load(x);\n}\n",
		"param.js":         "function walk(walk) {\n  return walk();\n}\n",
		"declarator.js":    "function walk() {\n  const walk = pick();\n  return walk();\n}\n",
		"destructure.ts":   "function walk() {\n  const { walk } = deps;\n  return walk();\n}\n",
		"nested-param.js":  "function walk(xs) {\n  return xs.map(function (walk) {\n    return walk();\n  });\n}\n",
		"arrow-param.js":   "function walk(xs) {\n  return xs.map((walk) => walk());\n}\n",
		"after-close.js":   "function walk() {\n  return 1;\n}\nwalk();\n",
		"in-string.js":     "function walk() {\n  return 'walk()' + `walk()` + /walk()/.source;\n}\n",
		"property.js":      "const api = {\n  walk: function (n) {\n    return this.walk(n);\n  },\n};\n",
		"catch-param.js":   "function fail() {\n  try { x(); } catch (fail) { fail(); }\n}\n",
		"private-other.ts": "class A {\n  #walk() {\n    return other.#walk();\n  }\n}\n",
	} {
		assertScriptFindings(t, name, body)
	}
}

// Negative: constructs that look like violations in text but are not: literals, comments,
// template text, regular expressions, methods named eval, bounded loops, handled errors and an
// abort in the entry point, a test file or module scope.
func TestScriptScanner_Negatives(t *testing.T) {
	for name, body := range map[string]string{
		"literals.js":   "// while (true) eval(x)\n/* for (;;) {} */\nconst s = 'eval(x)' + \"while (true)\";\nconst t = `for (;;) ${n}`;\nconst re = /while \\(true\\)|['\"{]/g;\n",
		"eval-name.ts":  "class Interp {\n  eval(node: Node) {\n    return interpreter.eval(node);\n  }\n}\nconst v = evaluate(x);\n",
		"bounded.js":    "for (let i = 0; i < 10; i++) { tick(); }\nwhile (i < n) { i++; }\n",
		"handled.ts":    "try { run(); } catch (e) { log(e); }\njob().catch((e) => report(e));\n",
		"entry.mjs":     "async function main() {\n  process.exit(await run());\n}\nmain().catch(() => process.exit(2));\nprocess.exit(0);\n",
		"cli.test.ts":   "function helper() {\n  process.exit(1);\n}\n",
		"expression.js": "const double = (x) =>\n  x * 2;\nconst id = x => x;\nitems.forEach((item) => {\n  use(item);\n});\n",
		"timer.js":      "setTimeout(() => tick(), 10);\nsetInterval(tick, 10);\n",
	} {
		assertScriptFindings(t, name, body)
	}
}

// Boundary: exactly the limit passes and one line over fails on the body's first line, also
// for a method and for a wrapped parameter list, whose signature lines lend it no length.
func TestScriptScanner_LOCBoundary(t *testing.T) {
	assertScriptFindings(t, "at.js", scriptFuncOfLOC("f", 60))
	assertScriptFindings(t, "over.js", scriptFuncOfLOC("f", 61), "HISS-04@1")
	body := strings.Repeat("    work();\n", 58)
	assertScriptFindings(t, "method.ts", "class A {\n  run(): void {\n"+body+"  }\n}\n")
	assertScriptFindings(t, "method-over.ts", "class A {\n  run(): void {\n"+body+"    work();\n  }\n}\n", "HISS-04@2")
	assertScriptFindings(t, "wrapped.ts", "function f(\n  a: number,\n  b: number,\n) {\n"+body+"}\n")
}

// Negative: braces inside template substitutions, regular expressions and object types do not
// desynchronise the function tracker, so the function after them is still measured exactly.
func TestScriptScanner_BracesInLiteralsDoNotDesync(t *testing.T) {
	prefix := "const a = `x ${ {k: 1}.k } ${`y ${'}'}`}`;\nconst re = /[{]\\}/;\nfunction typed(o: { a: number }): { b: string } {\n  return { b: '' };\n}\n"
	rep := assertScriptFindings(t, "desync.ts", prefix+scriptFuncOfLOC("after", 61), "HISS-04@6")
	if rep.Skips.Unparsed != 0 {
		t.Errorf("balanced file counted as unparsed: %+v", rep.Skips)
	}
}

// Boundary: a line at maxScriptLineBytes is hand-written source and is scanned; one byte more
// marks minified output, which is declined and counted as unscanned javascript, never as read.
func TestScriptScanner_MinifiedIsUnscannedNotClean(t *testing.T) {
	line := "eval(x);" + strings.Repeat(" ", maxScriptLineBytes-len("eval(x);"))
	rep := assertScriptFindings(t, "hand.js", line+"\n", "HISS-08@1")
	if rep.Coverage.FilesRead != 1 {
		t.Errorf("a %d-byte line must still be scanned: %+v", maxScriptLineBytes, rep.Coverage)
	}
	rep = assertScriptFindings(t, "bundle.js", line+" \n")
	c := rep.Coverage
	if c.FilesRead != 0 || c.UnscannedFiles != 1 || c.UnscannedLanguages["javascript"] != 1 {
		t.Errorf("a minified file must be unscanned javascript, got %+v", c)
	}
}

// Negative: a file whose braces do not balance was misread, so it is counted as not analysed
// (Skips.Unparsed) rather than reported clean, and the function left open is not measured.
func TestScriptScanner_UnbalancedFileIsUnparsed(t *testing.T) {
	rep := assertScriptFindings(t, "open.js", scriptFuncOfLOC("f", 70)[:40])
	if rep.Skips.Unparsed != 1 || !rep.Incomplete() {
		t.Errorf("an unbalanced file must be unparsed, got %+v", rep.Skips)
	}
	rep = assertScriptFindings(t, "extra.js", "}\n")
	if rep.Skips.Unparsed != 1 {
		t.Errorf("a stray closing brace must mark the file unparsed, got %+v", rep.Skips)
	}
}

// Negative: markup outside a component's script blocks is never read as script, and two
// script blocks on one component keep their line numbers.
func TestScriptScanner_SvelteScriptBlocksOnly(t *testing.T) {
	body := "<script context=\"module\">\n  export const x = 1;\n</script>\n<div on:click={() => { while (true) {} }}>eval(x)</div>\n<script>\n  function walk() { walk(); }\n</script>\n<style>p { color: red; }</style>\n"
	assertScriptFindings(t, "Two.svelte", body, "HISS-01@6")
	lines := svelteScriptLines(strings.Split("<script>b</script> c <script>d\n<p>a <script>e</script></p>\n<!--\n  the `<script>` tag\n-->", "\n"))
	if strings.Join(lines, "|") != "b||||" {
		t.Errorf("svelteScriptLines kept %q, want only the line-start block body", lines)
	}
	// A <script> written in a comment or an attribute string opens no block, so the braces
	// after it are never read as script and the component is not misreported as unbalanced.
	rep := assertScriptFindings(t, "Story.svelte", "<script>\n  const a = 1;\n</script>\n<!--\n  the `<script>` payload\n-->\n<Story args={{ s: '<script>x</' + 'script>{' }} />\n")
	if rep.Skips.Unparsed != 0 {
		t.Errorf("markup mentioning <script> made the component unparsed: %+v", rep.Skips)
	}
}

// Positive: the JavaScript mode of the literal stripper removes template text but keeps the
// code of its substitutions, removes regular expression literals only where an expression may
// start, and carries a template across lines.
func TestLiteralStripperScriptSyntax(t *testing.T) {
	for name, tc := range map[string]struct {
		lines []string
		want  []string
	}{
		"template keeps code":  {[]string{"a = `t ${f(x)} u`;"}, []string{"a = f(x);"}},
		"nested template":      {[]string{"a = `${`${g()}`}`;"}, []string{"a = g();"}},
		"template spans lines": {[]string{"a = `x", "${h()} y`; b();"}, []string{"a = ", "h(); b();"}},
		"regex after =":        {[]string{"r = /a'b/g; c();"}, []string{"r = ; c();"}},
		"division":             {[]string{"q = a / b / c;"}, []string{"q = a / b / c;"}},
		"regex after return":   {[]string{"return /}/.test(s);"}, []string{"return .test(s);"}},
		"regex class slash":    {[]string{"r = /[/]x/; d();"}, []string{"r = ; d();"}},
		"apostrophe string":    {[]string{"s = 'it''s'; e();"}, []string{"s = ; e();"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := &literalStripper{syn: scriptSyntax}
			for i, line := range tc.lines {
				if got := s.strip(line); got != tc.want[i] {
					t.Fatalf("line %d: strip(%q) = %q, want %q", i, line, got, tc.want[i])
				}
			}
			if s.open() {
				t.Fatalf("span left open after the last line: fence %q substitutions %v", s.fence, s.substitutions)
			}
		})
	}
}

// Boundary: substitutions nest up to maxTemplateNesting; one deeper is read as template text,
// so the stripper stays bounded and still closes the literal.
func TestLiteralStripperTemplateNestingBound(t *testing.T) {
	deep := strings.Repeat("`${", maxTemplateNesting+1) + strings.Repeat("}`", maxTemplateNesting+1)
	s := &literalStripper{syn: scriptSyntax}
	s.strip("a = " + deep + ";")
	if len(s.substitutions) > maxTemplateNesting {
		t.Fatalf("substitution stack grew past its bound: %d", len(s.substitutions))
	}
}
