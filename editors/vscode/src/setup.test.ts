import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import test from "node:test";
import { MCP_PROVIDER_ID } from "./mcp";
import { artifactPath, LSP_CLIENT_ID, LSP_DEFAULT_PATH, machineExecutable, MCP_DEFAULT_PATH, parseCapabilities, requireTrust, sentinelArguments, setupArguments, workspaceExecutable, workspaceGlob } from "./setup";
import { runCLI } from "./runner";

const sample = { client: "claude", mode: "merge", documentation: "https://example.invalid/docs", lifecycle: { state: "adapter-defined", definition_paths: [".claude/settings.json"], activation: "unverified" } };
const report = (clients: unknown[]) => JSON.stringify({ schema_version: 1, runtime_verified: false, clients });

type Manifest = {
  engines: { vscode: string };
  devDependencies: Record<string, string>;
  activationEvents: string[];
  capabilities: { untrustedWorkspaces: { restrictedConfigurations: string[] } };
  contributes: {
    commands: { command: string }[];
    configuration: { properties: Record<string, { default?: unknown; enum?: string[] } | undefined> };
    mcpServerDefinitionProviders?: { id: string; label: string }[];
  };
};

const readManifest = (): Manifest => JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "package.json"), "utf8"));
const readLock = (): { packages: Record<string, { version?: string; engines?: Record<string, string> }> } =>
  JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "package-lock.json"), "utf8"));
// registerMcpServerDefinitionProvider and McpStdioServerDefinition first ship in @types/vscode 1.101.0.
const MCP_API_FLOOR = [1, 101, 0];
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

test("both server paths are restricted settings and every restricted setting is contributed", () => {
  const manifest = readManifest();
  const properties = manifest.contributes.configuration.properties;
  const restricted = manifest.capabilities.untrustedWorkspaces.restrictedConfigurations;
  for (const key of ["standards.cli.path", "standards.lsp.path", "standards.mcp.path"]) assert.ok(restricted.includes(key), `${key} is not restricted`);
  for (const key of restricted) assert.ok(properties[key], `restricted ${key} is not a contributed setting`);
});

test("the engine floor carries the MCP provider API and equals the pinned host types", () => {
  const manifest = readManifest();
  const floor = /^\^(\d+)\.(\d+)\.(\d+)$/.exec(manifest.engines.vscode);
  assert.ok(floor, `engines.vscode ${manifest.engines.vscode} is not a caret floor`);
  const version = floor.slice(1).map(Number);
  // vsce refuses @types/vscode newer than engines.vscode; equal keeps the typed API and the floor in step.
  assert.equal(manifest.devDependencies["@types/vscode"], version.join("."));
  const lock = readLock();
  assert.equal(lock.packages["node_modules/@types/vscode"]?.version, version.join("."));
  assert.equal(lock.packages[""]?.engines?.vscode, manifest.engines.vscode);
  const below = MCP_API_FLOOR.findIndex((part, index) => version[index] !== part);
  assert.ok(below === -1 || version[below] > MCP_API_FLOOR[below], `floor ${version.join(".")} predates the MCP provider API`);
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
