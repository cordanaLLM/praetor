import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import test from "node:test";
import { artifactPath, LSP_CLIENT_ID, machineExecutable, parseCapabilities, requireTrust, setupArguments, workspaceGlob } from "./setup";
import { runCLI } from "./runner";

const sample = { client: "claude", mode: "merge", documentation: "https://example.invalid/docs", lifecycle: { state: "adapter-defined", definition_paths: [".claude/settings.json"], activation: "unverified" } };
const report = (clients: unknown[]) => JSON.stringify({ schema_version: 1, runtime_verified: false, clients });

type Manifest = {
  activationEvents: string[];
  capabilities: { untrustedWorkspaces: { restrictedConfigurations: string[] } };
  contributes: {
    commands: { command: string }[];
    configuration: { properties: Record<string, { default?: unknown; enum?: string[] } | undefined> };
    mcpServerDefinitionProviders?: unknown;
  };
};

const readManifest = (): Manifest => JSON.parse(fs.readFileSync(path.resolve(__dirname, "..", "package.json"), "utf8"));
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
  for (const key of ["lsp.enabled", "lsp.path", "cli.path", "verificationTimeoutSeconds"]) assert.ok(reads.has(key), `${key} is no longer read`);
  const properties = readManifest().contributes.configuration.properties;
  for (const [key, fallback] of reads) {
    const property = properties[`standards.${key}`];
    assert.ok(property, `standards.${key} is read but not contributed`);
    if (fallback !== undefined) assert.deepEqual(JSON.parse(fallback), property.default, `standards.${key} fallback differs from its contributed default`);
  }
});

test("no MCP setting is contributed; MCP configuration stays on the setup command", () => {
  const manifest = readManifest();
  // The pinned host API (engines.vscode ^1.90.0, @types/vscode 1.90.0) has no MCP server
  // registration, so the extension registers no server and nothing reads standards.mcp.*.
  const contributed = Object.keys(manifest.contributes.configuration.properties);
  assert.deepEqual(contributed.filter(key => key.startsWith("standards.mcp.")), []);
  assert.deepEqual([...settingsReadBy(extensionSource()).keys()].filter(key => key.startsWith("mcp.")), []);
  assert.equal(manifest.contributes.mcpServerDefinitionProviders, undefined);
  assert.ok(manifest.contributes.commands.some(entry => entry.command === "standards.setupAgents"));
  assert.ok(manifest.activationEvents.includes("onCommand:standards.setupAgents"));
});

test("the MCP removal keeps the same-shaped LSP settings and every restricted setting contributed", () => {
  const manifest = readManifest();
  const properties = manifest.contributes.configuration.properties;
  assert.equal(properties["standards.lsp.enabled"]?.default, true);
  assert.equal(properties["standards.lsp.path"]?.default, "${workspaceFolder}/bin/standards-lsp");
  const restricted = manifest.capabilities.untrustedWorkspaces.restrictedConfigurations;
  assert.ok(restricted.length > 0);
  for (const key of restricted) assert.ok(properties[key], `restricted ${key} is not a contributed setting`);
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
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
