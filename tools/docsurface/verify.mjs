// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { catalog } from "./catalog.mjs";

const MAX_SURFACES = 16;
const MAX_FILE_BYTES = 1_048_576;
const MAX_STRING_BYTES = 4_096;
const MAX_PATH_COMPONENTS = 64;
const MAX_SOURCE_BINDINGS = 320;
const MAX_FILE_READ_OPERATIONS = 1_024;
const MAX_REPOSITORY_ROOT_BYTES = 4_096;
const REPOSITORY_ROOT_TIMEOUT_MS = 10_000;
// A source-bound reference names its authorities; it never embeds their values.
// Fenced blocks (copied YAML/JSON policy) and Markdown tables (copied invariant
// matrices) are the two shapes a stale second specification takes.
const COPIED_POLICY_SHAPES = [
  { pattern: /^\s*(?:```|~~~)/mu, label: "a fenced code block" },
  { pattern: /^\s*\|/mu, label: "a Markdown table row" },
];

class SurfaceFailure extends Error {}

function fail(message) {
  throw new SurfaceFailure(message);
}

function boundedProcessText(value, label) {
  if (typeof value !== "string") {
    fail(`${label} is not UTF-8 text`);
  }
  if (Buffer.byteLength(value) > MAX_REPOSITORY_ROOT_BYTES) {
    fail(`${label} exceeds ${MAX_REPOSITORY_ROOT_BYTES} bytes`);
  }
  return value;
}

function repositoryRoot(run = spawnSync, realpath = fs.realpathSync) {
  let result;
  try {
    result = run("git", ["rev-parse", "--show-toplevel"], {
      encoding: "utf8",
      maxBuffer: MAX_REPOSITORY_ROOT_BYTES,
      stdio: "pipe",
      timeout: REPOSITORY_ROOT_TIMEOUT_MS,
      windowsHide: true,
    });
  } catch (error) {
    const detail = boundedProcessText(error?.message ?? String(error), "git start error");
    fail(`git failed to start: ${detail}`);
  }
  if (result === null || typeof result !== "object") {
    fail("git returned no process result");
  }
  if (result.error) {
    if (result.error.code === "ETIMEDOUT") {
      fail(`git exceeded ${REPOSITORY_ROOT_TIMEOUT_MS} ms`);
    }
    if (result.error.code === "ENOBUFS") {
      fail(`git output exceeds ${MAX_REPOSITORY_ROOT_BYTES} bytes`);
    }
    const detail = boundedProcessText(result.error.message, "git start error");
    fail(`git failed to start: ${detail}`);
  }
  if (result.signal) {
    fail(`git terminated by ${boundedProcessText(result.signal, "git signal")}`);
  }
  const stdout = boundedProcessText(result.stdout, "git stdout");
  const stderr = boundedProcessText(result.stderr, "git stderr");
  if (result.status !== 0) {
    const detail = stderr.trim();
    fail(`git exited ${result.status}${detail === "" ? "" : `: ${detail}`}`);
  }
  const candidate = stdout.trim();
  if (candidate === "") {
    fail("git rev-parse returned an empty repository root");
  }
  return canonicalRoot(candidate, realpath);
}

// Confinement compares canonical paths on both sides: a root reached through a
// directory link (macOS TMPDIR under /var -> /private/var) is the same tree.
function canonicalRoot(root, realpath = fs.realpathSync) {
  try {
    return realpath(root);
  } catch (error) {
    const detail = boundedProcessText(error?.message ?? String(error), "repository root error");
    fail(`cannot canonicalize repository root: ${detail}`);
  }
}

function inspectBoundedPath(root, relative, optional = false, lstatSync = fs.lstatSync) {
  const canonical = ownedPath(relative, "documentation path");
  const components = canonical.split("/");
  if (components.length > MAX_PATH_COMPONENTS) {
    fail(`${relative} exceeds ${MAX_PATH_COMPONENTS} path components`);
  }
  let current = root;
  for (let index = 0; index < components.length; index += 1) {
    current = path.join(current, components[index]);
    let stat;
    try {
      stat = lstatSync(current);
    } catch (error) {
      if (optional && error?.code === "ENOENT") {
        return null;
      }
      fail(`${relative} is missing or unreadable: ${error.message}`);
    }
    const logical = components.slice(0, index + 1).join("/");
    if (stat.isSymbolicLink()) {
      fail(`${relative} contains symbolic-link component ${logical}`);
    }
    if (index + 1 < components.length) {
      if (!stat.isDirectory()) {
        fail(`${relative} contains non-directory component ${logical}`);
      }
      continue;
    }
    if (!stat.isFile() || stat.size > MAX_FILE_BYTES) {
      fail(`${relative} must be a regular file no larger than ${MAX_FILE_BYTES} bytes`);
    }
    return { full: current, stat };
  }
  fail(`${relative} has no inspectable path component`);
}

function readBounded(root, relative, optional = false) {
  const inspected = inspectBoundedPath(root, relative, optional);
  if (inspected === null) {
    return null;
  }
  const { full } = inspected;
  const resolved = fs.realpathSync(full);
  const confined = path.relative(canonicalRoot(root), resolved);
  if (confined === ".." || confined.startsWith(`..${path.sep}`) || path.isAbsolute(confined)) {
    fail(`${relative} escapes the repository root`);
  }
  let descriptor;
  try {
    descriptor = fs.openSync(resolved, "r");
    const stat = fs.fstatSync(descriptor);
    if (!stat.isFile() || stat.size > MAX_FILE_BYTES) {
      fail(`${relative} changed into a non-file or exceeds ${MAX_FILE_BYTES} bytes`);
    }
    const buffer = Buffer.alloc(stat.size + 1);
    let consumed = 0;
    for (let operation = 0; operation < MAX_FILE_READ_OPERATIONS; operation += 1) {
      const count = fs.readSync(descriptor, buffer, consumed, buffer.length - consumed, null);
      consumed += count;
      if (count === 0) {
        return buffer.subarray(0, consumed).toString("utf8");
      }
      if (consumed === buffer.length) {
        fail(`${relative} changed while being read or exceeds ${MAX_FILE_BYTES} bytes`);
      }
    }
    fail(`${relative} exceeds ${MAX_FILE_READ_OPERATIONS} bounded read operations`);
  } catch (error) {
    if (error instanceof SurfaceFailure) {
      throw error;
    }
    fail(`${relative} cannot be read: ${error.message}`);
  } finally {
    if (descriptor !== undefined) {
      fs.closeSync(descriptor);
    }
  }
}

function normalizeText(value, relative) {
  const withoutCRLF = value.replaceAll("\r\n", "\n");
  if (withoutCRLF.includes("\r")) {
    fail(`${relative} has mixed or bare-CR line endings`);
  }
  return withoutCRLF;
}

function linkURL(link, input = catalog) {
  return new URL(link.path, input.pagesBase).href;
}

function renderLLMs(input = catalog) {
  const lines = [
    "# Praetor Documentation Index",
    "",
    "> Enterprise Fleet Governance, Repository-as-Code & Universal AI Agent Engineering Engine.",
  ];
  for (let sectionIndex = 0; sectionIndex < input.llmsSections.length; sectionIndex += 1) {
    const section = input.llmsSections[sectionIndex];
    lines.push("", `## ${section.title}`, "");
    for (let linkIndex = 0; linkIndex < section.links.length; linkIndex += 1) {
      const link = section.links[linkIndex];
      lines.push(`- [${link.label}](${linkURL(link, input)}): ${link.description}`);
    }
  }
  return `${lines.join("\n")}\n`;
}

function renderLLMsFull(input = catalog) {
  const lines = [
    `# ${input.llmsFull.title}`,
    "",
    `> ${input.llmsFull.notice}`,
    "",
    "Use `/llms.txt` for the concise public documentation index. This compatibility",
    "surface names the repository files that own current behavior so policy, required",
    "checks, and review settings cannot drift here as copied prose.",
    "",
    "## Canonical repository authorities",
    "",
  ];
  for (let index = 0; index < input.llmsFull.authorities.length; index += 1) {
    const authority = input.llmsFull.authorities[index];
    lines.push(`- **${authority.label}** — source: \`${authority.source}\`. ${authority.description}`);
  }
  lines.push("", "## Public documentation map");
  for (let sectionIndex = 0; sectionIndex < input.llmsSections.length; sectionIndex += 1) {
    const section = input.llmsSections[sectionIndex];
    lines.push("", `### ${section.title}`, "");
    for (let linkIndex = 0; linkIndex < section.links.length; linkIndex += 1) {
      const link = section.links[linkIndex];
      lines.push(`- [${link.label}](${linkURL(link, input)}) — source: \`${link.source}\`. ${link.description}`);
    }
  }
  return `${lines.join("\n")}\n`;
}

function boundedArray(value, label) {
  if (!Array.isArray(value) || value.length > MAX_SURFACES) {
    fail(`${label} must be an array bounded to ${MAX_SURFACES} entries`);
  }
  return value;
}

function boundedString(value, label) {
  if (typeof value !== "string" || value.trim() === "" || value.includes("\0") ||
      value.includes("\r") || value.includes("\n") || Buffer.byteLength(value) > MAX_STRING_BYTES) {
    fail(`${label} must be a non-empty single-line string bounded to ${MAX_STRING_BYTES} bytes`);
  }
  return value;
}

function unique(values, label) {
  if (new Set(values).size !== values.length) {
    fail(`${label} must be unique`);
  }
}

function ownedPath(value, label) {
  const relative = boundedString(value, label);
  if (path.posix.isAbsolute(relative) || relative.includes("\\") ||
      path.posix.normalize(relative) !== relative || relative.startsWith("../")) {
    fail(`${label} must be a canonical repository-relative path`);
  }
  return relative;
}

function pageRouteForSource(source) {
  const owned = ownedPath(source, "published source");
  if (!owned.startsWith("docs/")) {
    fail(`published source ${owned} must live under docs/`);
  }
  const relative = owned.slice("docs/".length);
  if (!relative.endsWith(".md")) {
    return relative;
  }
  const stem = relative.slice(0, -".md".length);
  const basename = path.posix.basename(stem);
  if (basename === "README" || basename === "index") {
    const parent = path.posix.dirname(stem);
    return parent === "." ? "" : `${parent}/`;
  }
  return `${stem}/`;
}

function verifyLLMs(input) {
  const sectionTitles = [];
  const targets = [];
  for (let sectionIndex = 0; sectionIndex < input.llmsSections.length; sectionIndex += 1) {
    const section = input.llmsSections[sectionIndex];
    sectionTitles.push(boundedString(section.title, `llmsSections[${sectionIndex}].title`));
    const links = boundedArray(section.links, `llmsSections[${sectionIndex}].links`);
    for (let linkIndex = 0; linkIndex < links.length; linkIndex += 1) {
      const link = links[linkIndex];
      boundedString(link.label, `llmsSections[${sectionIndex}].links[${linkIndex}].label`);
      boundedString(link.description, `llmsSections[${sectionIndex}].links[${linkIndex}].description`);
      const target = boundedString(link.path, `llmsSections[${sectionIndex}].links[${linkIndex}].path`);
      targets.push(target);
      if (target !== pageRouteForSource(link.source)) {
        fail(`llms target ${target} does not match its published source route`);
      }
    }
  }
  unique(sectionTitles, "llms section titles");
  unique(targets, "llms targets");
}

function verifyLLMsFull(input) {
  boundedString(input.llmsFull?.title, "llmsFull.title");
  boundedString(input.llmsFull?.notice, "llmsFull.notice");
  const authorities = boundedArray(input.llmsFull?.authorities, "llmsFull.authorities");
  const labels = [];
  const sources = [];
  for (let index = 0; index < authorities.length; index += 1) {
    const authority = authorities[index];
    labels.push(boundedString(authority.label, `llmsFull.authorities[${index}].label`));
    sources.push(ownedPath(authority.source, `llmsFull.authorities[${index}].source`));
    boundedString(authority.description, `llmsFull.authorities[${index}].description`);
  }
  unique(labels, "llmsFull authority labels");
  unique(sources, "llmsFull authority sources");
}

function verifyMachineDocsClaims(input) {
  const claims = boundedArray(input.machineDocsClaims, "machineDocsClaims");
  const identities = [];
  for (let index = 0; index < claims.length; index += 1) {
    const claim = claims[index];
    const target = ownedPath(claim.path, `machineDocsClaims[${index}].path`);
    const source = ownedPath(claim.source, `machineDocsClaims[${index}].source`);
    const statement = boundedString(claim.text, `machineDocsClaims[${index}].text`);
    identities.push(`${target}\0${statement}`);
    if (source !== "docs/llms-full.txt") {
      fail(`machineDocsClaims[${index}].source must be docs/llms-full.txt`);
    }
  }
  unique(identities, "machine-documentation claims");
}

function verifyNoCopiedPolicy(rendered, label) {
  for (let index = 0; index < COPIED_POLICY_SHAPES.length; index += 1) {
    const shape = COPIED_POLICY_SHAPES[index];
    if (shape.pattern.test(rendered)) {
      fail(`${label} contains ${shape.label}; name the authoritative source instead of copying it`);
    }
  }
}

function verifyPagesBase(input) {
  boundedString(input.pagesBase, "pagesBase");
  if (!input.pagesBase.startsWith("https://") || !input.pagesBase.endsWith("/")) {
    fail("pagesBase must be an absolute HTTPS directory URL");
  }
  try {
    if (new URL(input.pagesBase).href !== input.pagesBase) {
      fail("pagesBase must be a canonical absolute URL");
    }
  } catch (error) {
    if (error instanceof SurfaceFailure) {
      throw error;
    }
    fail(`pagesBase is invalid: ${error.message}`);
  }
}

function verifyCatalog(input = catalog) {
  if (input.version !== 1) {
    fail("documentation surface catalog version is invalid");
  }
  verifyPagesBase(input);
  boundedArray(input.llmsSections, "llmsSections");
  boundedArray(input.machineDocsClaims, "machineDocsClaims");
  verifyLLMs(input);
  verifyLLMsFull(input);
  verifyMachineDocsClaims(input);
  verifyNoCopiedPolicy(renderLLMs(input), "docs/llms.txt");
  verifyNoCopiedPolicy(renderLLMsFull(input), "docs/llms-full.txt");
}

function catalogSourceBindings(input = catalog) {
  const bindings = [];
  for (let sectionIndex = 0; sectionIndex < input.llmsSections.length; sectionIndex += 1) {
    const links = input.llmsSections[sectionIndex].links;
    for (let linkIndex = 0; linkIndex < links.length; linkIndex += 1) {
      bindings.push(links[linkIndex].source);
    }
  }
  for (let index = 0; index < input.llmsFull.authorities.length; index += 1) {
    bindings.push(input.llmsFull.authorities[index].source);
  }
  for (let index = 0; index < input.machineDocsClaims.length; index += 1) {
    bindings.push(input.machineDocsClaims[index].path, input.machineDocsClaims[index].source);
  }
  const uniqueBindings = [...new Set(bindings)];
  if (uniqueBindings.length > MAX_SOURCE_BINDINGS) {
    fail(`catalog source bindings exceed ${MAX_SOURCE_BINDINGS}`);
  }
  return uniqueBindings;
}

function verifySourceBindings(root) {
  const bindings = catalogSourceBindings();
  for (let index = 0; index < bindings.length; index += 1) {
    readBounded(root, bindings[index]);
  }
}

function verifyExactFile(root, relative, expected) {
  const actual = normalizeText(readBounded(root, relative), relative);
  if (actual !== expected) {
    fail(`${relative} differs from tools/docsurface/catalog.mjs`);
  }
}

function verifyMachineDocsClaimLines(root) {
  for (let index = 0; index < catalog.machineDocsClaims.length; index += 1) {
    const claim = catalog.machineDocsClaims[index];
    const content = `\n${normalizeText(readBounded(root, claim.path), claim.path)}\n`;
    const expected = `\n${claim.text}\n`;
    const first = content.indexOf(expected);
    if (first < 0 || content.indexOf(expected, first + expected.length) >= 0) {
      fail(`${claim.path} machine-documentation claim differs from tools/docsurface/catalog.mjs`);
    }
  }
}

function verifyRepository(root) {
  verifyCatalog();
  verifySourceBindings(root);
  verifyMachineDocsClaimLines(root);
  verifyExactFile(root, "docs/llms.txt", renderLLMs());
  verifyExactFile(root, "docs/llms-full.txt", renderLLMsFull());
}

function fixtureClaimLines(relative) {
  const lines = [];
  for (let index = 0; index < catalog.machineDocsClaims.length && index < MAX_SURFACES; index += 1) {
    if (catalog.machineDocsClaims[index].path === relative) {
      lines.push(catalog.machineDocsClaims[index].text);
    }
  }
  return lines.join("\n");
}

function writeFixture(root) {
  fs.mkdirSync(path.join(root, "docs"), { recursive: true });
  const bindings = catalogSourceBindings();
  for (let index = 0; index < bindings.length; index += 1) {
    const full = path.join(root, bindings[index]);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, "# Bound catalog source\n");
  }
  fs.writeFileSync(path.join(root, "README.md"), `# Fixture\n\n${fixtureClaimLines("README.md")}\n`);
  fs.writeFileSync(path.join(root, "docs", "index.md"), `# Docs\n\n${fixtureClaimLines("docs/index.md")}\n`);
  fs.writeFileSync(path.join(root, "docs", "llms.txt"), renderLLMs());
  fs.writeFileSync(path.join(root, "docs", "llms-full.txt"), renderLLMsFull());
}

function syntheticStat(kind, size = 1) {
  return {
    size,
    isDirectory: () => kind === "directory",
    isFile: () => kind === "file",
    isSymbolicLink: () => kind === "symlink",
  };
}

function syntheticLstat(entries, calls) {
  return (candidate) => {
    calls.push(candidate);
    const stat = entries.get(candidate);
    if (stat !== undefined) {
      return stat;
    }
    const error = new Error(`synthetic path is absent: ${candidate}`);
    error.code = "ENOENT";
    throw error;
  };
}

function portablePathFixtures() {
  const root = path.resolve("synthetic-docsurface-root");
  const direct = path.join(root, "README.md");
  const directCalls = [];
  assert.doesNotThrow(() => inspectBoundedPath(root, "README.md", false,
    syntheticLstat(new Map([[direct, syntheticStat("file")]]), directCalls)));
  assert.deepEqual(directCalls, [direct]);

  const docs = path.join(root, "docs");
  const standards = path.join(docs, "standards");
  const source = path.join(standards, "hiss-spec.md");
  const calls = [];
  const entries = new Map([
    [docs, syntheticStat("directory")],
    [standards, syntheticStat("directory")],
    [source, syntheticStat("file")],
  ]);
  const lstat = syntheticLstat(entries, calls);
  assert.doesNotThrow(() => inspectBoundedPath(root, "docs/standards/hiss-spec.md", false, lstat));
  assert.deepEqual(calls, [docs, standards, source]);

  entries.set(standards, syntheticStat("file"));
  assert.throws(() => inspectBoundedPath(root, "docs/standards/hiss-spec.md", false, lstat),
    /non-directory component docs\/standards/u);
  entries.set(standards, syntheticStat("symlink"));
  assert.throws(() => inspectBoundedPath(root, "docs/standards/hiss-spec.md", false, lstat),
    /symbolic-link component docs\/standards/u);
  entries.set(standards, syntheticStat("directory"));
  entries.set(source, syntheticStat("symlink"));
  assert.throws(() => inspectBoundedPath(root, "docs/standards/hiss-spec.md", false, lstat),
    /symbolic-link component docs\/standards\/hiss-spec\.md/u);

  const missing = path.join(docs, "missing.md");
  assert.equal(inspectBoundedPath(root, "docs/missing.md", true, syntheticLstat(entries, [])), null);
  assert.throws(() => inspectBoundedPath(root, "docs/missing.md", false, syntheticLstat(entries, [])),
    /docs\/missing\.md is missing/u);

  const atLimitParts = Array.from({ length: MAX_PATH_COMPONENTS }, (_, index) => `part-${index}`);
  const atLimit = atLimitParts.join("/");
  const atLimitLeaf = path.join(root, ...atLimitParts);
  const boundaryLstat = (candidate) => candidate === atLimitLeaf
    ? syntheticStat("file")
    : syntheticStat("directory");
  assert.doesNotThrow(() => inspectBoundedPath(root, atLimit, false, boundaryLstat));
  assert.throws(() => inspectBoundedPath(root, `${atLimit}/overflow`, false, boundaryLstat),
    /exceeds 64 path components/u);
}

function repositoryRootFixtures() {
  let invocation;
  const root = repositoryRoot((command, args, options) => {
    invocation = { command, args, options };
    return { error: null, signal: null, status: 0, stderr: "", stdout: "/synthetic/root\n" };
  }, (candidate) => candidate);
  assert.equal(root, "/synthetic/root");
  assert.equal(invocation.command, "git");
  assert.deepEqual(invocation.args, ["rev-parse", "--show-toplevel"]);
  assert.equal(invocation.options.timeout, REPOSITORY_ROOT_TIMEOUT_MS);
  assert.equal(invocation.options.maxBuffer, MAX_REPOSITORY_ROOT_BYTES);
  assert.equal(invocation.options.stdio, "pipe");

  const timedOut = Object.assign(new Error("synthetic timeout"), { code: "ETIMEDOUT" });
  assert.throws(() => repositoryRoot(() => ({
    error: timedOut, signal: "SIGTERM", status: null, stderr: "", stdout: "",
  }), (candidate) => candidate), /git exceeded 10000 ms/u);

  const absent = Object.assign(new Error("synthetic missing executable"), { code: "ENOENT" });
  assert.throws(() => repositoryRoot(() => ({
    error: absent, signal: null, status: null, stderr: "", stdout: "",
  }), (candidate) => candidate), /git failed to start: synthetic missing executable/u);

  assert.throws(() => repositoryRoot(() => ({
    error: null, signal: null, status: 2, stderr: "synthetic rev-parse failure\n", stdout: "",
  }), (candidate) => candidate), /git exited 2: synthetic rev-parse failure/u);
  assert.throws(() => repositoryRoot(() => ({
    error: null, signal: "SIGKILL", status: null, stderr: "", stdout: "",
  }), (candidate) => candidate), /git terminated by SIGKILL/u);

  const atLimit = `${"r".repeat(MAX_REPOSITORY_ROOT_BYTES - 1)}\n`;
  assert.doesNotThrow(() => repositoryRoot(() => ({
    error: null, signal: null, status: 0, stderr: "", stdout: atLimit,
  }), (candidate) => candidate));
  assert.throws(() => repositoryRoot(() => ({
    error: null, signal: null, status: 0, stderr: "", stdout: `${atLimit}x`,
  }), (candidate) => candidate), /git stdout exceeds 4096 bytes/u);
  assert.throws(() => repositoryRoot(() => ({
    error: null, signal: null, status: 2, stderr: "e".repeat(MAX_REPOSITORY_ROOT_BYTES + 1), stdout: "",
  }), (candidate) => candidate), /git stderr exceeds 4096 bytes/u);
}

function renderedSurfaceFixtures() {
  const llms = renderLLMs();
  const full = renderLLMsFull();
  assert.match(llms, /\[API Reference\]\(https:\/\/cordanallm\.github\.io\/praetor\/wiki\/API-Reference\/\)/u);
  assert.doesNotMatch(llms, /cordanallm\.github\.io\/praetor\/[^)]+\.md\)/u);
  assert.doesNotMatch(llms, /github\.com/u);
  assert.match(full, /source: `\.standards\.yaml`/u);
  assert.match(full, /source: `\.github\/rulesets\/main\.json`/u);
  assert.match(full, /source: `docs\/standards\/hiss-spec\.md`/u);
  assert.doesNotMatch(full, /required_approving_review_count|require_code_owner_review|"context"\s*:/u);
  assert.doesNotMatch(full, /^\| \*\*HISS-\d{2}\*\*/mu);
  assert.doesNotMatch(full, /```/u);
}

function repositoryFixtures(root) {
  writeFixture(root);
  assert.doesNotThrow(() => verifyRepository(root));

  const fullPath = path.join(root, "docs", "llms-full.txt");
  fs.appendFileSync(fullPath, "\n```json\n{\"required_approving_review_count\": 1}\n```\n");
  assert.throws(() => verifyRepository(root), /docs\/llms-full\.txt differs/u);

  writeFixture(root);
  fs.writeFileSync(fullPath, "# Stale copied policy\n");
  assert.throws(() => verifyRepository(root), /docs\/llms-full\.txt differs/u);

  writeFixture(root);
  const llmsPath = path.join(root, "docs", "llms.txt");
  fs.writeFileSync(llmsPath, renderLLMs().replace("wiki/API-Reference/", "wiki/API-Reference.md"));
  assert.throws(() => verifyRepository(root), /docs\/llms\.txt differs/u);

  writeFixture(root);
  const indexPath = path.join(root, "docs", "index.md");
  fs.writeFileSync(indexPath, readBounded(root, "docs/index.md").replace(
    "Source-bound compatibility map", "Complete unrolled specification",
  ));
  assert.throws(() => verifyRepository(root), /docs\/index\.md machine-documentation claim differs/u);

  writeFixture(root);
  fs.rmSync(path.join(root, ".github", "rulesets", "main.json"));
  assert.throws(() => verifyRepository(root), /\.github\/rulesets\/main\.json is missing/u);

  writeFixture(root);
  fs.rmSync(path.join(root, "docs", "standards", "hiss-spec.md"));
  assert.throws(() => verifyRepository(root), /docs\/standards\/hiss-spec\.md is missing/u);

  writeFixture(root);
  const boundedPath = path.join(root, "docs", "bounded-read.txt");
  fs.writeFileSync(boundedPath, Buffer.alloc(MAX_FILE_BYTES, 0x61));
  assert.equal(readBounded(root, "docs/bounded-read.txt").length, MAX_FILE_BYTES);
  fs.appendFileSync(boundedPath, "x");
  assert.throws(() => readBounded(root, "docs/bounded-read.txt"), /no larger than 1048576 bytes/u);
}

function linkedRootFixtures(base, root) {
  writeFixture(root);
  const linked = path.join(base, "linked-root");
  // "junction" keeps the directory link unprivileged on Windows; other
  // platforms ignore the type and create an ordinary symbolic link.
  fs.symlinkSync(root, linked, "junction");
  assert.doesNotThrow(() => verifyRepository(linked));
  assert.equal(readBounded(linked, "README.md"), readBounded(root, "README.md"));
  assert.equal(canonicalRoot(linked), canonicalRoot(root));
  fs.writeFileSync(path.join(root, "docs", "llms-full.txt"), "# Stale copied policy\n");
  assert.throws(() => verifyRepository(linked), /docs\/llms-full\.txt differs/u);
  assert.throws(() => canonicalRoot(path.join(base, "missing-root")), /cannot canonicalize repository root/u);
}

function withLink(sectionIndex, linkIndex, change) {
  return catalog.llmsSections.map((section, currentSection) => ({
    ...section,
    links: section.links.map((link, currentLink) => currentSection === sectionIndex &&
      currentLink === linkIndex ? { ...link, ...change } : link),
  }));
}

function catalogFixtures() {
  assert.doesNotThrow(() => verifyCatalog());
  const overCap = {
    ...catalog,
    llmsSections: new Array(MAX_SURFACES + 1).fill(catalog.llmsSections[0]),
  };
  assert.throws(() => verifyCatalog(overCap), /bounded to 16 entries/u);
  const atCapLinks = Array.from({ length: MAX_SURFACES }, (_, index) => ({
    label: `Guide ${index}`, path: `guide-${index}/`, source: `docs/guide-${index}.md`, description: "Bounded.",
  }));
  const atCap = { ...catalog, llmsSections: [{ title: "Bounded", links: atCapLinks }] };
  assert.doesNotThrow(() => verifyCatalog(atCap));
  const duplicateAuthority = {
    ...catalog,
    llmsFull: {
      ...catalog.llmsFull,
      authorities: [...catalog.llmsFull.authorities, catalog.llmsFull.authorities[0]],
    },
  };
  assert.throws(() => verifyCatalog(duplicateAuthority), /llmsFull authority labels must be unique/u);
  assert.throws(() => verifyCatalog({ ...catalog, llmsSections: withLink(1, 2, { path: "wiki/API-Reference.md" }) }),
    /does not match its published source route/u);
  assert.throws(() => verifyCatalog({ ...catalog, llmsSections: withLink(0, 0, { label: "" }) }),
    /non-empty single-line string/u);
  const copiedTable = { ...catalog, llmsSections: withLink(0, 0, { description: "x\n| **HISS-12** | SemVer |" }) };
  assert.throws(() => verifyCatalog(copiedTable), /non-empty single-line string/u);
}

function copiedPolicyFixtures() {
  const reference = renderLLMsFull();
  assert.doesNotThrow(() => verifyNoCopiedPolicy(reference, "docs/llms-full.txt"));
  assert.throws(() => verifyNoCopiedPolicy(
    `${reference}\n\`\`\`json\n{"required_approving_review_count": 1}\n\`\`\`\n`, "docs/llms-full.txt",
  ), /docs\/llms-full\.txt contains a fenced code block/u);
  assert.throws(() => verifyNoCopiedPolicy(
    `${reference}\n| **HISS-12** | Semantic Versioning |\n`, "docs/llms-full.txt",
  ), /docs\/llms-full\.txt contains a Markdown table row/u);
  assert.doesNotThrow(() => verifyNoCopiedPolicy("- Inline `|` and ``` stay prose.\n", "boundary"));
}

function selfTest() {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "praetor-docsurface-"));
  const root = path.join(base, "repository");
  try {
    fs.mkdirSync(root);
    repositoryRootFixtures();
    portablePathFixtures();
    catalogFixtures();
    copiedPolicyFixtures();
    renderedSurfaceFixtures();
    repositoryFixtures(root);
    linkedRootFixtures(base, root);
    process.stdout.write("documentation-surfaces: positive, negative, and boundary fixtures pass\n");
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
}

function main() {
  if (process.argv.length === 3 && process.argv[2] === "--self-test") {
    selfTest();
    return;
  }
  if (process.argv.length !== 2) {
    fail("usage: node tools/docsurface/verify.mjs [--self-test]");
  }
  verifyRepository(repositoryRoot());
  process.stdout.write("documentation-surfaces: machine-documentation catalog parity verified\n");
}

try {
  main();
} catch (error) {
  process.stderr.write(`documentation-surfaces: ${error.message}\n`);
  process.exitCode = error instanceof SurfaceFailure || error instanceof assert.AssertionError ? 1 : 2;
}
