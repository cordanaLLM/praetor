import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import test from "node:test";
import { artifactPath, machineExecutable, parseCapabilities, requireTrust, setupArguments, workspaceGlob } from "./setup";
import { runCLI } from "./runner";

const sample = { client: "claude", mode: "merge", documentation: "https://example.invalid/docs", lifecycle: { state: "adapter-defined", definition_paths: [".claude/settings.json"], activation: "unverified" } };
const report = (clients: unknown[]) => JSON.stringify({ schema_version: 1, runtime_verified: false, clients });

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
