// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const MAX_FILES = 4_096;
const MAX_FILE_BYTES = 1_048_576;
const MAX_TOTAL_BYTES = 67_108_864;
const MAX_GIT_OUTPUT_BYTES = 16_777_216;
const MAX_CAPTURE_BYTES = 1_048_576;
const MAX_DIAGNOSTIC_OUTPUT_BYTES = 65_536;
const MAX_DIAGNOSTIC_OUTPUT_LINES = 200;
const MAX_FAILURE_DETAIL_BYTES = 4_096;
const MAX_FAILURE_DETAIL_LINES = 20;
const MAX_PATH_BYTES = 4_096;
const MAX_TRACKED_ENTRIES = 65_536;
const MAX_TRACKED_SYMLINKS = 2_048;
const MAX_SYMLINK_TARGET_BYTES = 4_096;
const TRUNCATION_MARKER_BYTES = 256;
const MAX_COMMAND_BYTES = 24_000;
const DEFAULT_COMMAND_TIMEOUT_MS = 120_000;
const INSTALL_TIMEOUT_MS = 300_000;
const MARKDOWN_SUFFIXES = [".md", ".markdown", ".mdx", ".md.tmpl", ".markdown.tmpl", ".mdx.tmpl"];
const SCRATCH_ROOTS = new Set([".workingdir", ".workingdir2"]);
const FILESYSTEM_SYMLINK_UNAVAILABLE = new Set(["EPERM", "EACCES", "ENOSYS"]);
const TOOL_FILES = [
  "package.json",
  "package-lock.json",
  "markdownlint-cli2.yaml",
  "no-private-scratch-links.mjs",
];
// Style-only exclusions. These files are generated release/agent surfaces or
// carry a separate format contract (caveman, fixture bytes, or vendored source).
// The private-link rule never uses these exclusions: it receives every Markdown
// path returned by the bounded Git inventory below.
const STYLE_EXCLUDED_FILES = new Set([
  ".github/copilot-instructions.md",
  "AGENTS.md",
  "CHANGELOG.md",
  "CLAUDE.md",
]);
const STYLE_EXCLUDED_PREFIXES = [
  ".agents/agents/",
  ".agents/plugins/",
  ".agents/skills/",
  ".claude/",
  ".codex/",
  ".cursor/",
  ".gemini/",
  ".github/agents/",
  ".paperclip/",
  ".standards/worktrees/",
  ".workingdir/",
  ".workingdir2/",
  "internal/caveman/testdata/",
  "node_modules/",
  "vendor/",
];

class GateFailure extends Error {
  constructor(message, status = 2) {
    super(message);
    this.status = status;
  }
}

function fail(message, status = 2) {
  throw new GateFailure(message, status);
}

function boundedOutput(value, maxBytes, maxLines) {
  let cursor = 0;
  let bytes = 0;
  let lines = 0;
  let output = "";
  while (cursor < value.length && lines < maxLines && bytes < maxBytes) {
    const newline = value.indexOf("\n", cursor);
    const end = newline < 0 ? value.length : newline + 1;
    const segment = value.slice(cursor, end);
    const segmentBytes = Buffer.byteLength(segment);
    if (bytes + segmentBytes > maxBytes) {
      break;
    }
    output += segment;
    bytes += segmentBytes;
    lines += 1;
    cursor = end;
  }
  return { output, bytes, lines, truncated: cursor < value.length };
}

function outputBudget() {
  return {
    bytes: MAX_DIAGNOSTIC_OUTPUT_BYTES - TRUNCATION_MARKER_BYTES,
    lines: MAX_DIAGNOSTIC_OUTPUT_LINES - 1,
    truncated: false,
  };
}

function emitBounded(value, stream, budget, label) {
  if (!value || budget.truncated) {
    return budget.truncated;
  }
  const marker = `markdown-governance: ${label} diagnostics truncated at ` +
    `${MAX_DIAGNOSTIC_OUTPUT_LINES} lines/${MAX_DIAGNOSTIC_OUTPUT_BYTES} bytes\n`;
  assert.ok(Buffer.byteLength(marker) <= TRUNCATION_MARKER_BYTES);
  const selected = boundedOutput(value, budget.bytes, budget.lines);
  stream.write(selected.output);
  budget.bytes -= selected.bytes;
  budget.lines -= selected.lines;
  if (selected.truncated) {
    stream.write(marker);
    budget.truncated = true;
  }
  return selected.truncated;
}

function command(commandName, args, options = {}) {
  const timeout = options.timeout ?? DEFAULT_COMMAND_TIMEOUT_MS;
  const result = spawnSync(commandName, args, {
    cwd: options.cwd,
    encoding: options.binary ? null : "utf8",
    env: { ...process.env, ...options.env },
    input: options.input,
    maxBuffer: options.maxBuffer ?? MAX_CAPTURE_BYTES,
    stdio: "pipe",
    timeout,
    windowsHide: true,
  });
  if (result.error) {
    if (result.error.code === "ETIMEDOUT") {
      fail(`${commandName} exceeded ${timeout} ms`);
    }
    fail(`${commandName} failed to start or exceeded ${options.maxBuffer ?? MAX_CAPTURE_BYTES} captured bytes: ${result.error.message}`);
  }
  if (result.signal) {
    fail(`${commandName} terminated by ${result.signal}`);
  }
  if (result.status !== 0 && !options.allowFailure) {
    const rawOutput = result.stderr?.length ? result.stderr : result.stdout;
    const rawDetail = (Buffer.isBuffer(rawOutput) ? rawOutput.toString("utf8") : rawOutput || "").trim();
    const detail = boundedOutput(rawDetail, MAX_FAILURE_DETAIL_BYTES, MAX_FAILURE_DETAIL_LINES).output.trim();
    fail(`${commandName} exited ${result.status}${detail ? `: ${detail}` : ""}`);
  }
  return result;
}

function repositoryRoot() {
  const result = command("git", ["rev-parse", "--show-toplevel"], { maxBuffer: MAX_GIT_OUTPUT_BYTES });
  const root = result.stdout.trim();
  if (root === "") {
    fail("git rev-parse returned an empty repository root");
  }
  return fs.realpathSync(root);
}

function isStyleSelected(relative) {
  const normalized = relative.replaceAll("\\", "/");
  const lower = normalized.toLowerCase();
  if (!MARKDOWN_SUFFIXES.some((suffix) => lower.endsWith(suffix))) {
    return false;
  }
  if (STYLE_EXCLUDED_FILES.has(normalized)) {
    return false;
  }
  if (lower.split("/").includes("node_modules")) {
    return false;
  }
  return !STYLE_EXCLUDED_PREFIXES.some((prefix) => normalized.startsWith(prefix));
}

function parseTrackedSymlinkRecords(records) {
  if (records.length > MAX_TRACKED_ENTRIES) {
    fail(`tracked inventory has ${records.length} entries; maximum is ${MAX_TRACKED_ENTRIES}`);
  }
  const symlinks = [];
  for (let index = 0; index < records.length && index < MAX_TRACKED_ENTRIES; index += 1) {
    const separator = records[index].indexOf("\t");
    if (separator < 0) {
      fail(`malformed tracked inventory entry at index ${index}`);
    }
    const metadata = /^(\d{6}) ([0-9a-f]{40}|[0-9a-f]{64}) ([0-3])$/u.exec(
      records[index].slice(0, separator),
    );
    const relative = records[index].slice(separator + 1);
    if (metadata === null || relative === "" || Buffer.byteLength(relative) > MAX_PATH_BYTES ||
      /[\0\t\r\n]/u.test(relative)) {
      fail(`unsafe or malformed tracked inventory entry at index ${index}`);
    }
    if (metadata[1] === "120000") {
      symlinks.push({ oid: metadata[2], relative });
      if (symlinks.length > MAX_TRACKED_SYMLINKS) {
        fail(`tracked symlink inventory exceeds ${MAX_TRACKED_SYMLINKS} entries`);
      }
    }
  }
  return symlinks;
}

function trackedSymlinkRecords(root) {
  const result = command("git", ["ls-files", "-z", "--stage"], {
    cwd: root,
    maxBuffer: MAX_GIT_OUTPUT_BYTES,
  });
  return parseTrackedSymlinkRecords(result.stdout.split("\0").filter(Boolean));
}

function readSymlinkTargets(root, records) {
  const objectIDs = [...new Set(records.map((record) => record.oid))];
  if (objectIDs.length === 0) {
    return new Map();
  }
  const result = command("git", ["cat-file", "--batch"], {
    binary: true,
    cwd: root,
    input: `${objectIDs.join("\n")}\n`,
    maxBuffer: MAX_GIT_OUTPUT_BYTES,
  });
  const targets = new Map();
  let cursor = 0;
  for (let index = 0; index < objectIDs.length && index < MAX_TRACKED_SYMLINKS; index += 1) {
    const headerEnd = result.stdout.indexOf(0x0a, cursor);
    if (headerEnd < 0) {
      fail(`git cat-file omitted symlink header at index ${index}`);
    }
    const header = result.stdout.subarray(cursor, headerEnd).toString("ascii");
    const parsed = /^([0-9a-f]{40}|[0-9a-f]{64}) blob ([0-9]+)$/u.exec(header);
    if (parsed === null || parsed[1] !== objectIDs[index]) {
      fail(`git cat-file returned malformed symlink header at index ${index}`);
    }
    const size = Number.parseInt(parsed[2], 10);
    if (!Number.isSafeInteger(size) || size > MAX_SYMLINK_TARGET_BYTES) {
      fail(`tracked symlink target exceeds ${MAX_SYMLINK_TARGET_BYTES} bytes at index ${index}`);
    }
    const contentStart = headerEnd + 1;
    const contentEnd = contentStart + size;
    if (contentEnd >= result.stdout.length || result.stdout[contentEnd] !== 0x0a) {
      fail(`git cat-file truncated symlink target at index ${index}`);
    }
    const bytes = result.stdout.subarray(contentStart, contentEnd);
    const target = bytes.toString("utf8");
    if (!Buffer.from(target, "utf8").equals(bytes)) {
      fail(`tracked symlink target is not UTF-8 at index ${index}`);
    }
    targets.set(objectIDs[index], target);
    cursor = contentEnd + 1;
  }
  if (cursor !== result.stdout.length) {
    fail("git cat-file returned trailing symlink data");
  }
  return targets;
}

function privateScratchRoot(target) {
  if (Buffer.byteLength(target) > MAX_SYMLINK_TARGET_BYTES || /[\0\r\n]/u.test(target)) {
    fail("tracked symlink has an unsafe or oversized target");
  }
  const normalized = path.posix.normalize(target.replaceAll("\\", "/"));
  const parts = normalized.split("/").filter(Boolean);
  return parts.map((part) => part.toLowerCase()).find((part) => SCRATCH_ROOTS.has(part)) ?? null;
}

function auditTrackedSymlinkTargets(root) {
  const records = trackedSymlinkRecords(root);
  const targets = readSymlinkTargets(root, records);
  for (let index = 0; index < records.length && index < MAX_TRACKED_SYMLINKS; index += 1) {
    const target = targets.get(records[index].oid);
    if (target === undefined) {
      fail(`tracked symlink ${records[index].relative} lacks an indexed target`);
    }
    const scratchRoot = privateScratchRoot(target);
    if (scratchRoot !== null) {
      fail(`tracked symlink ${records[index].relative} targets private scratch root ${scratchRoot}`);
    }
  }
}

function inventory(root) {
  auditTrackedSymlinkTargets(root);
  const result = command("git", [
    "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
    ...MARKDOWN_SUFFIXES.map((suffix) => `:(icase,glob)**/*${suffix}`),
  ], { cwd: root, maxBuffer: MAX_GIT_OUTPUT_BYTES });
  const selected = result.stdout.split("\0").filter(Boolean).sort();
  if (selected.length > MAX_FILES) {
    fail(`Markdown inventory has ${selected.length} files; maximum is ${MAX_FILES}`);
  }
  let totalBytes = 0;
  for (let index = 0; index < selected.length && index < MAX_FILES; index += 1) {
    const relative = selected[index];
    if (Buffer.byteLength(relative) > MAX_PATH_BYTES || /[\0\r\n]/u.test(relative)) {
      fail(`refusing unsafe or oversized Markdown path at inventory index ${index}`);
    }
    const full = path.join(root, relative);
    const stat = fs.lstatSync(full);
    if (stat.isSymbolicLink()) {
      fail(`refusing symbolic-link Markdown source ${relative}`);
    }
    if (!stat.isFile()) {
      fail(`refusing non-file Markdown source ${relative}`);
    }
    const resolved = fs.realpathSync(full);
    const confined = path.relative(root, resolved);
    if (confined === ".." || confined.startsWith(`..${path.sep}`) || path.isAbsolute(confined)) {
      fail(`Markdown source escapes repository root: ${relative}`);
    }
    if (stat.size > MAX_FILE_BYTES) {
      fail(`${relative} is ${stat.size} bytes; per-file maximum is ${MAX_FILE_BYTES}`);
    }
    totalBytes += stat.size;
    if (totalBytes > MAX_TOTAL_BYTES) {
      fail(`Markdown inventory is ${totalBytes} bytes; maximum is ${MAX_TOTAL_BYTES}`);
    }
  }
  return selected;
}

function install(toolDir, temporary) {
  for (let index = 0; index < TOOL_FILES.length && index < TOOL_FILES.length; index += 1) {
    fs.copyFileSync(path.join(toolDir, TOOL_FILES[index]), path.join(temporary, TOOL_FILES[index]));
  }
  const npm = process.platform === "win32" ? "npm.cmd" : "npm";
  command(npm, ["ci", "--ignore-scripts", "--no-audit", "--no-fund"], {
    cwd: temporary,
    timeout: INSTALL_TIMEOUT_MS,
  });
}

function filesystemSymlinkUnavailable(error) {
  return FILESYSTEM_SYMLINK_UNAVAILABLE.has(error?.code);
}

function filesystemSymlinkSkipDiagnostic(platform, code) {
  return `Markdown filesystem symlink fixture skipped on ${platform}: ${code}; ` +
    "alternate coverage: Git-index 120000 symlink target fixture\n";
}

function inventorySelfTest(temporary) {
  const fixture = path.join(temporary, "inventory-fixture");
  fs.mkdirSync(path.join(fixture, "docs"), { recursive: true });
  fs.mkdirSync(path.join(fixture, "templates"), { recursive: true });
  fs.mkdirSync(path.join(fixture, "vendor"), { recursive: true });
  fs.mkdirSync(path.join(fixture, ".workingdir"), { recursive: true });
  fs.writeFileSync(path.join(fixture, ".gitignore"), "/.workingdir/\n**/node_modules/\n");
  fs.writeFileSync(path.join(fixture, "AGENTS.md"),
    "#Malformed generated surface\n\n[private](.workingdir/OPEN.md)\n");
  fs.writeFileSync(path.join(fixture, "README.md"), "# Root\n");
  fs.writeFileSync(path.join(fixture, "docs", "guide.md"), "# Guide\n");
  fs.writeFileSync(path.join(fixture, "notes.markdown"), "# Notes\n");
  fs.writeFileSync(path.join(fixture, "docs", "page.mdx"),
    "# MDX page\n\n[public](../README.md)\n\n<Card href=\"../README.md\">Public</Card>\n");
  fs.writeFileSync(path.join(fixture, "templates", "README.md.tmpl"), "# Template\n");
  fs.writeFileSync(path.join(fixture, "templates", "Card.mdx.tmpl"), "# MDX template\n");
  fs.writeFileSync(path.join(fixture, "vendor", "README.md"), "#Malformed vendor surface\n");
  fs.writeFileSync(path.join(fixture, ".workingdir", "private.md"), "# Private\n");
  command("git", ["init", "--quiet"], { cwd: fixture });
  command("git", ["add", "--", ".gitignore", "AGENTS.md", "README.md", "docs/guide.md",
    "vendor/README.md"], { cwd: fixture });
  const allFiles = inventory(fs.realpathSync(fixture));
  assert.deepEqual(allFiles, ["AGENTS.md", "README.md", "docs/guide.md", "docs/page.mdx",
    "notes.markdown", "templates/Card.mdx.tmpl", "templates/README.md.tmpl", "vendor/README.md"]);
  for (const code of FILESYSTEM_SYMLINK_UNAVAILABLE) {
    assert.equal(filesystemSymlinkUnavailable({ code }), true);
  }
  assert.equal(filesystemSymlinkUnavailable({ code: "EIO" }), false);
  assert.equal(filesystemSymlinkUnavailable(null), false);
  assert.equal(filesystemSymlinkSkipDiagnostic("win32", "EPERM"),
    "Markdown filesystem symlink fixture skipped on win32: EPERM; " +
    "alternate coverage: Git-index 120000 symlink target fixture\n");
  const linkedMarkdown = path.join(fixture, "docs", "linked.md");
  try {
    fs.symlinkSync("../README.md", linkedMarkdown);
    assert.throws(() => inventory(fs.realpathSync(fixture)), /refusing symbolic-link Markdown source/u);
    fs.unlinkSync(linkedMarkdown);
  } catch (error) {
    if (!filesystemSymlinkUnavailable(error)) {
      throw error;
    }
    process.stdout.write(filesystemSymlinkSkipDiagnostic(process.platform, error.code));
  }
  const aliasPath = path.join(fixture, "docs", "public.txt");
  const aliasMarkdown = path.join(fixture, "docs", "alias.md");
  fs.writeFileSync(aliasPath, "../.workingdir/private.txt");
  fs.writeFileSync(aliasMarkdown, "# Alias\n\n--8<-- \"docs/public.txt\"\n");
  const aliasOID = command("git", ["hash-object", "-w", "docs/public.txt"], { cwd: fixture }).stdout.trim();
  command("git", ["update-index", "--add", "--cacheinfo", `120000,${aliasOID},docs/public.txt`], {
    cwd: fixture,
  });
  command("git", ["add", "--", "docs/alias.md"], { cwd: fixture });
  assert.throws(() => inventory(fs.realpathSync(fixture)),
    /tracked symlink docs\/public\.txt targets private scratch root \.workingdir/u);
  command("git", ["update-index", "--force-remove", "--", "docs/public.txt", "docs/alias.md"], { cwd: fixture });
  fs.unlinkSync(aliasPath);
  fs.unlinkSync(aliasMarkdown);
  const regularRecord = `100644 ${"0".repeat(40)} 0\tREADME.md`;
  assert.doesNotThrow(() => parseTrackedSymlinkRecords(new Array(MAX_TRACKED_ENTRIES).fill(regularRecord)));
  assert.throws(() => parseTrackedSymlinkRecords(new Array(MAX_TRACKED_ENTRIES + 1).fill(regularRecord)),
    /tracked inventory has 65537 entries/u);
  const symlinkRecord = `120000 ${"0".repeat(40)} 0\tdocs/public.txt`;
  assert.equal(parseTrackedSymlinkRecords(new Array(MAX_TRACKED_SYMLINKS).fill(symlinkRecord)).length,
    MAX_TRACKED_SYMLINKS);
  assert.throws(() => parseTrackedSymlinkRecords(new Array(MAX_TRACKED_SYMLINKS + 1).fill(symlinkRecord)),
    /tracked symlink inventory exceeds 2048 entries/u);
  assert.equal(privateScratchRoot("x".repeat(MAX_SYMLINK_TARGET_BYTES)), null);
  assert.throws(() => privateScratchRoot("x".repeat(MAX_SYMLINK_TARGET_BYTES + 1)),
    /unsafe or oversized target/u);
  assert.equal(runScratchRule(fixture, temporary, allFiles, false, false), 1);
  fs.writeFileSync(path.join(fixture, "AGENTS.md"), "#Malformed generated surface\n");
  assert.equal(runScratchRule(fixture, temporary, allFiles, false, false), 0);
  fs.writeFileSync(path.join(fixture, "docs", "page.mdx"),
    "# MDX page\n\n[private](../.workingdir/OPEN.md)\n");
  assert.equal(runScratchRule(fixture, temporary, allFiles, false, false), 1);
  fs.writeFileSync(path.join(fixture, "docs", "page.mdx"),
    "# MDX page\n\n<Card href=\"../.workingdir/OPEN.md\">Private</Card>\n");
  assert.equal(runScratchRule(fixture, temporary, allFiles, false, false), 1);
  fs.writeFileSync(path.join(fixture, "docs", "page.mdx"),
    "# MDX page\n\n[public](../README.md)\n\n<Card href=\"../README.md\">Public</Card>\n");
  const styleFiles = allFiles.filter(isStyleSelected);
  assert.deepEqual(styleFiles, ["README.md", "docs/guide.md", "docs/page.mdx", "notes.markdown",
    "templates/Card.mdx.tmpl", "templates/README.md.tmpl"]);
  assert.equal(runMarkdownlint(fixture, temporary, styleFiles, false), 0);
  fs.writeFileSync(path.join(fixture, "docs", "malformed.md"), "#Malformed public Markdown\n");
  const malformedFiles = inventory(fs.realpathSync(fixture)).filter(isStyleSelected);
  assert.equal(runMarkdownlint(fixture, temporary, malformedFiles, false), 1);
  const exactLines = "x\n".repeat(MAX_DIAGNOSTIC_OUTPUT_LINES);
  assert.equal(boundedOutput(exactLines, MAX_DIAGNOSTIC_OUTPUT_BYTES, MAX_DIAGNOSTIC_OUTPUT_LINES).truncated, false);
  assert.equal(boundedOutput(`${exactLines}x\n`, MAX_DIAGNOSTIC_OUTPUT_BYTES,
    MAX_DIAGNOSTIC_OUTPUT_LINES).truncated, true);
  const exactBytes = "x".repeat(MAX_DIAGNOSTIC_OUTPUT_BYTES);
  assert.equal(boundedOutput(exactBytes, MAX_DIAGNOSTIC_OUTPUT_BYTES,
    MAX_DIAGNOSTIC_OUTPUT_LINES).truncated, false);
  assert.equal(boundedOutput(`${exactBytes}x`, MAX_DIAGNOSTIC_OUTPUT_BYTES,
    MAX_DIAGNOSTIC_OUTPUT_LINES).truncated, true);
  process.stdout.write("Markdown style fixtures: valid, malformed, generated/vendor exclusions, inventory bounds pass\n");
}

function runScratchRule(root, temporary, files, selfTest, emitDiagnostics = true) {
  const rule = path.join(temporary, "no-private-scratch-links.mjs");
  const args = selfTest ? [rule, "--self-test"] : [rule, root, path.join(temporary, "inventory.json")];
  if (!selfTest) {
    fs.writeFileSync(args[2], `${JSON.stringify(files)}\n`, { mode: 0o600 });
  }
  const result = command(process.execPath, args, { cwd: root, allowFailure: true });
  const budget = outputBudget();
  let overflow = false;
  if (emitDiagnostics && (selfTest || result.status !== 0)) {
    overflow ||= emitBounded(result.stdout, process.stdout, budget, "private-link");
    overflow ||= emitBounded(result.stderr, process.stderr, budget, "private-link");
  }
  return overflow ? 2 : result.status ?? 2;
}

function markdownlintEntry(temporary) {
  const metadata = JSON.parse(fs.readFileSync(path.join(temporary, "node_modules", "markdownlint-cli2", "package.json"), "utf8"));
  if (metadata.version !== "0.23.2" || metadata.bin?.["markdownlint-cli2"] !== "./markdownlint-cli2-bin.mjs") {
    fail("installed markdownlint-cli2 package does not match locked 0.23.2 binary contract");
  }
  return path.join(temporary, "node_modules", "markdownlint-cli2", metadata.bin["markdownlint-cli2"]);
}

function batches(files) {
  const result = [];
  let current = [];
  let bytes = 0;
  for (let index = 0; index < files.length && index < MAX_FILES; index += 1) {
    const literal = `:${files[index]}`;
    if (current.length > 0 && bytes + Buffer.byteLength(literal) + 1 > MAX_COMMAND_BYTES) {
      result.push(current);
      current = [];
      bytes = 0;
    }
    current.push(literal);
    bytes += Buffer.byteLength(literal) + 1;
  }
  if (current.length > 0) {
    result.push(current);
  }
  return result;
}

function runMarkdownlint(root, temporary, files, emitDiagnostics = true) {
  const cli = markdownlintEntry(temporary);
  const config = path.join(temporary, "markdownlint-cli2.yaml");
  let failed = false;
  let overflow = false;
  const budget = outputBudget();
  for (const batch of batches(files)) {
    const result = command(process.execPath, [cli, "--config", config, "--no-globs", ...batch], {
      cwd: root,
      allowFailure: true,
    });
    failed ||= result.status !== 0;
    if (emitDiagnostics && result.status !== 0) {
      overflow ||= emitBounded(result.stdout, process.stdout, budget, "markdownlint");
      overflow ||= emitBounded(result.stderr, process.stderr, budget, "markdownlint");
    }
  }
  return overflow ? 2 : failed ? 1 : 0;
}

function main() {
  const selfTest = process.argv.length === 3 && process.argv[2] === "--self-test";
  if (!selfTest && process.argv.length !== 2) {
    fail("usage: node tools/markdownlint/verify.mjs [--self-test]");
  }
  const toolDir = path.dirname(fileURLToPath(import.meta.url));
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "praetor-markdownlint-"));
  try {
    install(toolDir, temporary);
    if (selfTest) {
      inventorySelfTest(temporary);
      process.exitCode = runScratchRule(process.cwd(), temporary, [], true);
      return;
    }
    const root = repositoryRoot();
    const scratchFiles = inventory(root);
    const styleFiles = scratchFiles.filter(isStyleSelected);
    const scratchStatus = runScratchRule(root, temporary, scratchFiles, false);
    const lintStatus = runMarkdownlint(root, temporary, styleFiles);
    process.stdout.write(`markdown-governance: styled ${styleFiles.length} public Markdown files; ` +
      `checked ${scratchFiles.length} tracked/non-ignored Markdown files for private links\n`);
    process.exitCode = scratchStatus === 0 && lintStatus === 0 ? 0 :
      scratchStatus > 1 || lintStatus > 1 ? 2 : 1;
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}

try {
  main();
} catch (error) {
  process.stderr.write(`markdown-governance: ${error.message}\n`);
  process.exitCode = error instanceof GateFailure ? error.status : 2;
}
