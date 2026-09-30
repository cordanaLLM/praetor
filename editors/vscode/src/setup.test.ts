import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import test from "node:test";
import { MCP_PROVIDER_ID } from "./mcp";
import { artifactPath, boundedIsFile, checkedLaunchFiles, commandAvailable, globLiteral, LSP_CLIENT_ID, LSP_DEFAULT_PATH, machineExecutable, MAX_MARKER_FOLDERS, MCP_DEFAULT_PATH, parseCapabilities, PRAETOR_MARKERS, praetorWorkspace, requireTrust, sentinelArguments, setupArguments, workspaceExecutable, workspaceGlob } from "./setup";
import { runCLI } from "./runner";

const sample = { client: "claude", mode: "merge", documentation: "https://example.invalid/docs", lifecycle: { state: "adapter-defined", definition_paths: [".claude/settings.json"], activation: "unverified" } };
const report = (clients: unknown[]) => JSON.stringify({ schema_version: 1, runtime_verified: false, clients });

type Manifest = {
  engines: { vscode: string };
  dependencies: Record<string, string>;
  devDependencies: Record<string, string>;
  activationEvents: string[];
  capabilities: { untrustedWorkspaces: { restrictedConfigurations: string[] } };
  contributes: {
    commands: { command: string }[];
    configuration: { properties: Record<string, { default?: unknown; enum?: string[] } | undefined> };
    mcpServerDefinitionProviders?: { id: string; label: string; when?: string }[];
  };
};

const readManifest = (): Manifest => JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "package.json"), "utf8"));
const readLock = (): { packages: Record<string, { version?: string; engines?: Record<string, string> }> } =>
  JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "package-lock.json"), "utf8"));
// registerMcpServerDefinitionProvider and McpStdioServerDefinition first ship in @types/vscode 1.101.0.
const MCP_API_FLOOR = [1, 101, 0];
// The contribution `when` clause first ships in VS Code 1.105.0 (extensionMcpDiscovery.ts,
// microsoft/vscode#268097); below it MCP discovery activates the extension everywhere.
const WHEN_CLAUSE_FLOOR = [1, 105, 0];
// Antigravity IDE reports VS Code 1.107.0; a floor above it stops the fork receiving the extension.
const TRAILING_FORK_HOST = [1, 107, 0];
const compareVersions = (left: number[], right: number[]): number => {
  const index = left.findIndex((part, position) => part !== right[position]);
  return index === -1 ? 0 : left[index] - right[index];
};
// caretFloor reads the lowest version a `^major.minor.patch` engines range admits.
const caretFloor = (range: string | undefined): number[] | undefined => {
  const floor = /^\^(\d+)\.(\d+)\.(\d+)$/.exec(range ?? "");
  return floor ? floor.slice(1).map(Number) : undefined;
};
const extensionSource = (): string => fs.readFileSync(path.resolve(__dirname, "..", "src", "extension.ts"), "utf8");

// Settings the extension reads from its configuration section: `.get<T>("key", fallback)` and
// `.inspect<T>("key")`, mapped to the fallback literal (undefined for inspect).
function settingsReadBy(source: string): Map<string, string | undefined> {
  const reads = new Map<string, string | undefined>();
  for (const match of source.matchAll(/\.(?:get|inspect)<[^>]+>\("([^"]+)"(?:,\s*([^)]+))?\)/g)) reads.set(match[1], match[2]);
  return reads;
}

test("every setting the extension reads is contributed with the same default", () => {
  const source = extensionSource();
  const sections = [...source.matchAll(/getConfiguration\("([^"]*)"/g)].map(match => match[1]);
  assert.ok(sections.length > 0, "extension reads no configuration section");
  assert.deepEqual([...new Set(sections)], ["standards"]);
  const reads = settingsReadBy(source);
  for (const key of ["lsp.enabled", "lsp.path", "mcp.enabled", "mcp.path", "cli.path", "verificationTimeoutSeconds"]) assert.ok(reads.has(key), `${key} is no longer read`);
  const properties = readManifest().contributes.configuration.properties;
  for (const [key, fallback] of reads) {
    const property = properties[`standards.${key}`];
    assert.ok(property, `standards.${key} is read but not contributed`);
    if (fallback !== undefined) assert.deepEqual(JSON.parse(fallback), property.default, `standards.${key} fallback differs from its contributed default`);
  }
});

test("every contributed setting has a reader", () => {
  const reads = settingsReadBy(extensionSource());
  const unread = Object.keys(readManifest().contributes.configuration.properties).filter(key =>
    // vscode-languageclient reads `<client id>.trace.server` itself; every other key is read by extension.ts.
    key !== `${LSP_CLIENT_ID}.trace.server` && !(key.startsWith("standards.") && reads.has(key.slice("standards.".length))));
  assert.deepEqual(unread, []);
  // BUG-1032: modelTier had no reader and is gone; headroomMB is now read by the sentinel command.
  const properties = readManifest().contributes.configuration.properties;
  assert.equal(properties["standards.modelTier"], undefined);
  assert.ok(reads.has("sentinel.headroomMB"));
});

test("the sentinel headroom setting becomes one measured CLI call", () => {
  assert.deepEqual(sentinelArguments(1024), ["sentinel", "--min-free-mb=1024"]);
  assert.deepEqual(sentinelArguments(1), ["sentinel", "--min-free-mb=1"]);
  // Zero would make the CLI skip the check, so it is refused here, as are fractions and non-numbers.
  for (const value of [0, -1, 1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1, "1024", undefined, null]) {
    assert.throws(() => sentinelArguments(value), `accepted ${String(value)}`);
  }
  assert.equal(readManifest().contributes.configuration.properties["standards.sentinel.headroomMB"]?.default, 1024);
});

test("the MCP server definition provider is contributed under the id the extension registers", () => {
  const manifest = readManifest();
  assert.deepEqual(manifest.contributes.mcpServerDefinitionProviders?.map(entry => entry.id), [MCP_PROVIDER_ID]);
  assert.ok(manifest.contributes.mcpServerDefinitionProviders?.[0]?.label.trim());
  assert.match(extensionSource(), /registerMcpServerDefinitionProvider\(MCP_PROVIDER_ID,/);
  const properties = manifest.contributes.configuration.properties;
  assert.equal(properties["standards.mcp.enabled"]?.default, true);
  assert.equal(properties["standards.mcp.path"]?.default, MCP_DEFAULT_PATH);
  assert.equal(properties["standards.lsp.enabled"]?.default, true);
  assert.equal(properties["standards.lsp.path"]?.default, LSP_DEFAULT_PATH);
  // The setup command still prepares configuration for every other client.
  assert.ok(manifest.contributes.commands.some(entry => entry.command === "standards.setupAgents"));
  assert.ok(manifest.activationEvents.includes("onCommand:standards.setupAgents"));
});

test("VS Code registers the MCP collection, and so activates the extension, only for a trusted, enabled folder", () => {
  // VS Code turns each contributed collection into an onMcpCollection:<id> activation event and
  // registers the collection only while its `when` holds (extensionMcpDiscovery.ts), so without a
  // `when` MCP discovery activates the extension in every workspace.
  const [collection] = readManifest().contributes.mcpServerDefinitionProviders ?? [];
  const clauses = collection?.when?.split(" && ") ?? [];
  assert.deepEqual(clauses, ["isWorkspaceTrusted", "config.standards.mcp.enabled", "workspaceFolderCount > 0"]);
});

test("the status bar markers are the workspaceContains activation files", () => {
  const events = readManifest().activationEvents.filter(event => event.startsWith("workspaceContains:"));
  assert.deepEqual(events.map(event => event.slice("workspaceContains:".length)), [...PRAETOR_MARKERS]);
});

test("a marker file at a folder root marks a Praetor workspace", async () => {
  const folders = ["/work/app", "/work/tools"];
  for (const marker of PRAETOR_MARKERS) {
    const present = new Set([path.join("/work/tools", marker)]);
    assert.equal(await praetorWorkspace(folders, async file => present.has(file)), true, marker);
  }
  assert.equal(await praetorWorkspace(folders, async () => false), false);
  assert.equal(await praetorWorkspace([], async () => true), false);
  // Only the first MAX_MARKER_FOLDERS folders are inspected.
  const many = Array.from({ length: MAX_MARKER_FOLDERS + 1 }, (_, index) => `/work/f${index}`);
  const at = (index: number) => new Set([path.join(many[index], "AGENTS.md")]);
  assert.equal(await praetorWorkspace(many, async file => at(MAX_MARKER_FOLDERS - 1).has(file)), true);
  assert.equal(await praetorWorkspace(many, async file => at(MAX_MARKER_FOLDERS).has(file)), false);
});

test("a server command starts only when an absolute path names a file", async () => {
  const checked: string[] = [];
  const files = (present: string[]) => async (file: string) => { checked.push(file); return present.includes(file); };
  assert.equal(await commandAvailable("/r/bin/standards-lsp", "linux", files(["/r/bin/standards-lsp"])), true);
  assert.equal(await commandAvailable("/r/bin/standards-lsp", "darwin", files([])), false);
  assert.equal(await commandAvailable("/r/bin/standards-lsp", "linux", files(["/r/bin/standards-lsp.exe"])), false);
  const windows = "C:\\r\\bin\\standards-lsp";
  assert.equal(await commandAvailable(windows, "win32", files([`${windows}.exe`])), true);
  assert.equal(await commandAvailable(windows, "win32", files([])), false);
  checked.length = 0;
  for (const command of ["standards-lsp", "bin/standards-lsp", "bin\\standards-lsp"]) {
    assert.equal(await commandAvailable(command, "win32", files([])), true, command);
    assert.equal(await commandAvailable(command, "linux", files([])), true, command);
  }
  assert.deepEqual(checked, [], "a command name or relative path was checked on disk");
});

test("the checked launch files are the candidates of an absolute command only", () => {
  assert.deepEqual(checkedLaunchFiles("/r/bin/standards-mcp", "linux"), ["/r/bin/standards-mcp"]);
  const windows = "C:\\r\\bin\\standards-mcp";
  assert.deepEqual(checkedLaunchFiles(windows, "win32"), [windows, `${windows}.com`, `${windows}.exe`]);
  assert.deepEqual(checkedLaunchFiles(`${windows}.exe`, "win32"), [`${windows}.exe`]);
  for (const command of ["standards-mcp", "bin/standards-mcp", "bin\\standards-mcp"]) {
    assert.deepEqual(checkedLaunchFiles(command, "win32"), [], command);
    assert.deepEqual(checkedLaunchFiles(command, "linux"), [], command);
  }
});

test("glob symbols in a watched file name match literally", () => {
  assert.equal(globLiteral("standards-mcp"), "standards-mcp");
  assert.equal(globLiteral("a*b?c[d]{e}"), "a[*]b[?]c[[]d[]][{]e[}]");
  assert.equal(globLiteral(""), "");
  assert.equal(workspaceGlob("/r/a*b/"), "/r/a[*]b/**/*.go");
});

test("a file probe that stalls or fails counts as absent", async () => {
  assert.equal(await boundedIsFile(async () => true, 50)("/r/f"), true);
  assert.equal(await boundedIsFile(async () => false, 50)("/r/f"), false);
  assert.equal(await boundedIsFile(async () => { throw new Error("EACCES"); }, 50)("/r/f"), false);
  assert.equal(await boundedIsFile(() => { throw new Error("sync"); }, 50)("/r/f"), false);
  // A probe that never settles is abandoned at the timeout.
  const started = Date.now();
  assert.equal(await boundedIsFile(() => new Promise<boolean>(() => undefined), 20)("/r/f"), false);
  assert.ok(Date.now() - started < 1000, "the stalled probe was awaited past its timeout");
  // Boundary: an answer inside the window is kept, one after it is not.
  const late = (ms: number) => () => new Promise<boolean>(resolve => setTimeout(() => resolve(true), ms));
  assert.equal(await boundedIsFile(late(5), 200)("/r/f"), true);
  assert.equal(await boundedIsFile(late(200), 5)("/r/f"), false);
});

test("the LSP start and the status bar are gated on what exists", () => {
  const source = extensionSource();
  // The LSP resolves its command through commandAvailable before a LanguageClient exists, and the
  // status item is shown only through praetorWorkspace, so an activation by MCP discovery or a Go
  // file in an unrelated workspace neither pops "Praetor LSP unavailable" nor shows the item.
  assert.match(source, /commandAvailable\(executable, process\.platform, isFile\)/);
  assert.ok(source.indexOf("await lspExecutable(folder)") < source.indexOf("new LanguageClient("));
  assert.deepEqual([...source.matchAll(/status\.show\(\)/g)].length, 1);
  assert.match(source, /if \(await praetorWorkspace\(folders, isFile\)\) status\.show\(\);/);
  // Every probe those gates make is bounded, and the MCP provider follows its launch files and the
  // active editor's folder.
  assert.match(source, /const isFile = boundedIsFile\(/);
  assert.equal([...source.matchAll(/\bstat\(/g)].length, 1, "an unbounded stat call was added");
  assert.match(source, /onDidChangeActiveTextEditor\(\(\) => provider\.activeFolderChanged\(\)\)/);
  assert.match(source, /watchLaunchFiles\(context, provider\);/);
});

test("both server paths are restricted settings and every restricted setting is contributed", () => {
  const manifest = readManifest();
  const properties = manifest.contributes.configuration.properties;
  const restricted = manifest.capabilities.untrustedWorkspaces.restrictedConfigurations;
  for (const key of ["standards.cli.path", "standards.lsp.path", "standards.mcp.path"]) assert.ok(restricted.includes(key), `${key} is not restricted`);
  for (const key of restricted) assert.ok(properties[key], `restricted ${key} is not a contributed setting`);
});

test("the engine floor carries the MCP provider API and equals the pinned host types", () => {
  const manifest = readManifest();
  const version = caretFloor(manifest.engines.vscode);
  assert.ok(version, `engines.vscode ${manifest.engines.vscode} is not a caret floor`);
  // vsce refuses @types/vscode newer than engines.vscode; equal keeps the typed API and the floor in step.
  assert.equal(manifest.devDependencies["@types/vscode"], version.join("."));
  const lock = readLock();
  assert.equal(lock.packages["node_modules/@types/vscode"]?.version, version.join("."));
  assert.equal(lock.packages[""]?.engines?.vscode, manifest.engines.vscode);
  assert.ok(compareVersions(version, MCP_API_FLOOR) >= 0, `floor ${version.join(".")} predates the MCP provider API`);
});

test("the engine floor honours the MCP when clause and still admits trailing forks", () => {
  const version = caretFloor(readManifest().engines.vscode);
  assert.ok(version);
  assert.ok(compareVersions(version, WHEN_CLAUSE_FLOOR) >= 0, `floor ${version.join(".")} ignores the contribution when clause`);
  assert.ok(compareVersions(version, TRAILING_FORK_HOST) <= 0, `floor ${version.join(".")} drops VS Code ${TRAILING_FORK_HOST.join(".")} forks`);
  assert.equal(compareVersions([1, 107, 0], [1, 107, 0]), 0);
  assert.ok(compareVersions([1, 108, 0], TRAILING_FORK_HOST) > 0 && compareVersions([1, 104, 9], WHEN_CLAUSE_FLOOR) < 0);
});

// vscode-languageclient declares the lowest host it supports (10.x raised it to ^1.91.0); a client
// floor above engines.vscode would load in hosts the client does not support.
test("the locked language client supports every host the engine floor admits", () => {
  const manifest = readManifest();
  const pinned = manifest.dependencies["vscode-languageclient"];
  const locked = readLock().packages["node_modules/vscode-languageclient"];
  assert.equal(locked?.version, pinned);
  const host = caretFloor(manifest.engines.vscode);
  const client = caretFloor(locked?.engines?.vscode);
  assert.ok(host && client, `engines ${manifest.engines.vscode} and ${locked?.engines?.vscode} are not both caret floors`);
  assert.ok(compareVersions(client, host) <= 0, `vscode-languageclient ${pinned} needs VS Code ${client.join(".")}, above the ${host.join(".")} floor`);
  // Boundary: an equal floor is admitted. Negative: one minor above is not; a range without a caret floor yields none.
  assert.equal(compareVersions(host, host), 0);
  assert.ok(compareVersions([host[0], host[1] + 1, 0], host) > 0);
  assert.equal(caretFloor(">=1.91.0"), undefined);
  assert.equal(caretFloor(undefined), undefined);
});

// Since 10.0 the client publishes only package `exports` (no main/typings); tsconfig resolves them
// with node16, and the compiled require must reach the node entry while deep paths stay closed.
test("the language client entry resolves through its package exports", () => {
  assert.match(require.resolve("vscode-languageclient/node"), /vscode-languageclient[\\/]lib[\\/]node[\\/]main\.js$/);
  assert.throws(() => require.resolve("vscode-languageclient/lib/node/main"), { code: "ERR_PACKAGE_PATH_NOT_EXPORTED" });
  // "node" (node10) ignores exports, so tsc finds no types for vscode-languageclient/node.
  const tsconfig = JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "tsconfig.json"), "utf8"));
  assert.ok(["node16", "node20", "nodenext"].includes(String(tsconfig.compilerOptions.moduleResolution).toLowerCase()),
    `moduleResolution ${tsconfig.compilerOptions.moduleResolution} ignores package exports`);
});

test("server paths fall back to the contributed default and bind the folder", () => {
  const root = path.join(os.tmpdir(), "a ${workspaceFolder} b");
  assert.equal(workspaceExecutable("${workspaceFolder}/tools/mcp", MCP_DEFAULT_PATH, "/r"), "/r/tools/mcp");
  assert.equal(workspaceExecutable("/opt/praetor/standards-lsp", LSP_DEFAULT_PATH, "/r"), "/opt/praetor/standards-lsp");
  for (const value of [undefined, "", "  ", null, 7]) assert.equal(workspaceExecutable(value, MCP_DEFAULT_PATH, "/r"), "/r/bin/standards-mcp");
  // The folder path is inserted literally: never re-expanded, and `$&` is not a replacement pattern.
  assert.equal(workspaceExecutable(undefined, LSP_DEFAULT_PATH, root), `${root}/bin/standards-lsp`);
  assert.equal(workspaceExecutable(undefined, MCP_DEFAULT_PATH, "/r$&x$'"), "/r$&x$'/bin/standards-mcp");
  assert.equal(workspaceExecutable("${workspaceFolder}:${workspaceFolder}", LSP_DEFAULT_PATH, "/r"), "/r:/r");
});

test("the language client reads its trace level from the contributed setting", () => {
  const properties = readManifest().contributes.configuration.properties;
  // vscode-languageclient reads `<client id>.trace.server`; that key must be the contributed one.
  const trace = properties[`${LSP_CLIENT_ID}.trace.server`];
  assert.ok(trace, `${LSP_CLIENT_ID}.trace.server is not a contributed setting`);
  assert.deepEqual(trace.enum, ["off", "messages", "verbose"]);
  // The former client id read a key nothing contributes, so the setting had no effect.
  assert.equal(properties["standardsLSP.trace.server"], undefined);
  // Every other LSP setting is read from the "standards" section; the id stays inside it.
  assert.ok(LSP_CLIENT_ID.startsWith("standards."));
});

test("configuration authority and strict capability boundaries", () => {
  assert.throws(() => requireTrust(false));
  requireTrust(true);
  assert.equal(machineExecutable(undefined, "praetorctl"), "praetorctl");
  for (const value of ["", "bin/praetorctl", "praetorctl --unsafe", null]) assert.throws(() => machineExecutable(value, "praetorctl"));
  assert.equal(parseCapabilities(report([sample]))[0].client, "claude");
  assert.throws(() => parseCapabilities(report([{ ...sample, mode: ["native"] }])));
  assert.throws(() => parseCapabilities(report([{ ...sample, lifecycle: { ...sample.lifecycle, state: ["adapter-defined"] } }])));
  for (const raw of [report([]), report([sample, sample]), report([null]), report([{ ...sample, lifecycle: { activation: "verified" } }]), report(Array(65).fill(sample)), '{"schema_version":2}']) {
    assert.throws(() => parseCapabilities(raw));
  }
  const maximum = Array.from({ length: 64 }, (_, i) => ({ ...sample, client: `client-${i}` }));
  assert.equal(parseCapabilities(report(maximum)).length, 64);
});

test("new artifact destinations and native mode cannot select unsupported apply", () => {
  const root = os.tmpdir();
  assert.equal(artifactPath(root, "new-config"), path.join(root, "new-config"));
  for (const name of ["", ".", "..", "../outside", "a/b", "x".repeat(101)]) assert.throws(() => artifactPath(root, name));
  const native = parseCapabilities(report([{ ...sample, client: "codex", mode: "native" }]))[0];
  assert.throws(() => setupArguments(native, true, path.join(root, "registry.json"), path.join(root, "new"), path.join(root, "config")));
  assert.throws(() => setupArguments(native, false, path.join(root, "registry.json"), path.join(root, "new"), path.join(root, "config")));
  assert.equal(workspaceGlob("/tmp/a[b]/{c}"), "/tmp/a[[]b[]]/[{]c[}]/**/*.go");
});

test("extension setup arguments execute against actual shared Go CLI", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "praetor editor setup "));
  const repo = path.resolve(__dirname, "../../..");
  const binary = path.join(root, process.platform === "win32" ? "praetorctl.exe" : "praetorctl");
  try {
    execFileSync("go", ["build", "-o", binary, "./cmd/standardsctl"], { cwd: repo, timeout: 120_000, stdio: "pipe" });
    const inventory = await runCLI(binary, ["clients", "capabilities"], root);
    assert.equal(inventory.exitCode, 0);
    const clients = parseCapabilities(inventory.stdout);
    const client = clients.find(item => item.client === "gemini");
    assert.ok(client);
    const registry = path.join(root, "registry $(literal).json");
    const config = path.join(root, "client config.json");
    fs.writeFileSync(registry, JSON.stringify({ version: 1, servers: [{ name: "fixture", command: process.execPath, args: ["--version"] }] }), { mode: 0o600 });
    const before = JSON.stringify({ theme: "preserve", hooks: { BeforeTool: [] } });
    fs.writeFileSync(config, before, { mode: 0o600 });
    const prepared = artifactPath(root, "prepared");
    const result = await runCLI(binary, setupArguments(client, false, registry, prepared, config), root);
    assert.equal(result.exitCode, 0, result.stderr);
    assert.equal(fs.readFileSync(config, "utf8"), before);
    assert.ok(fs.existsSync(path.join(prepared, "plan.json")));
    const applied = artifactPath(root, "applied");
    const application = await runCLI(binary, setupArguments(client, true, registry, applied, config), root);
    assert.equal(application.exitCode, 0, application.stderr);
    const actual = JSON.parse(fs.readFileSync(config, "utf8"));
    assert.equal(actual.theme, "preserve");
    assert.deepEqual(actual.hooks, { BeforeTool: [] });
    assert.equal(actual.mcpServers.fixture.command, process.execPath);
    assert.equal(fs.readFileSync(path.join(applied, "config.before"), "utf8"), before);
    const retry = await runCLI(binary, setupArguments(client, true, registry, applied, config), root);
    assert.notEqual(retry.exitCode, 0, "Existing backup destination was silently reused");
    // The sentinel command's arguments reach the Go measurement: 1 MiB passes wherever memory is
    // read (/proc/meminfo, Windows API), macOS reports it unmeasured, 1 PiB is never free, and one
    // MiB past the CLI limit is refused.
    const headroom = await runCLI(binary, sentinelArguments(1), root);
    if (process.platform === "darwin") assert.match(headroom.stdout, /\[UNMEASURED\]/);
    else assert.equal(headroom.exitCode, 0, headroom.stderr);
    assert.notEqual((await runCLI(binary, sentinelArguments(1_073_741_824), root)).exitCode, 0);
    const outside = await runCLI(binary, sentinelArguments(1_073_741_825), root);
    assert.notEqual(outside.exitCode, 0);
    assert.match(outside.stderr, /exceeds/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
