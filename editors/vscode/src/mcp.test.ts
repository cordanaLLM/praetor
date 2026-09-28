import assert from "node:assert/strict";
import test from "node:test";
import type { Event } from "vscode";
import { LaunchFileWatch, MCP_SERVER_LABEL, McpFolder, McpLaunch, mcpLaunch, McpProviderHost, McpSettings, StandardsMcpProvider, stickyFolder } from "./mcp";
import { launchCandidates } from "./setup";

const root = "/work/repo";
const defaultCommand = `${root}/bin/standards-mcp`;

function emitter() {
  const listeners: (() => unknown)[] = [];
  const event: Event<void> = listener => { listeners.push(() => listener()); return { dispose: () => undefined }; };
  return { event, fire: () => { for (const listener of listeners) listener(); } };
}

type Fixture = { trusted?: boolean; folder?: McpFolder | undefined; settings?: McpSettings; files?: string[]; platform?: NodeJS.Platform };

function provider(fixture: Fixture = {}) {
  const checked: string[] = [];
  const defined: McpLaunch[] = [];
  const host: McpProviderHost<McpLaunch> = {
    trusted: () => fixture.trusted ?? true,
    folder: () => ("folder" in fixture ? fixture.folder : { fsPath: root }),
    settings: () => fixture.settings ?? { enabled: true, path: undefined },
    isFile: async file => { checked.push(file); return (fixture.files ?? [defaultCommand]).includes(file); },
    platform: fixture.platform ?? "linux",
    define: launch => { defined.push(launch); return launch; },
  };
  const changes = emitter();
  return { provider: new StandardsMcpProvider(host, changes), checked, defined };
}

test("the provider offers one stdio definition for the workspace standards-mcp", async () => {
  const { provider: subject, defined } = provider();
  const definitions = await subject.provideMcpServerDefinitions();
  assert.equal(definitions.length, 1);
  assert.deepEqual(defined, [{ label: MCP_SERVER_LABEL, command: defaultCommand, args: ["-transport=stdio", "-root", root], cwd: root }]);
});

test("a configured path is resolved against the folder", async () => {
  const command = `${root}/tools/praetor-mcp`;
  const { provider: subject } = provider({ settings: { enabled: true, path: "${workspaceFolder}/tools/praetor-mcp" }, files: [command] });
  const [definition] = await subject.provideMcpServerDefinitions();
  assert.equal(definition?.command, command);
});

test("disabled, untrusted, folderless and missing servers are not offered", async () => {
  const cases: Fixture[] = [
    { settings: { enabled: false, path: undefined } },
    { trusted: false },
    { folder: undefined },
    { files: [] },
  ];
  for (const fixture of cases) {
    const { provider: subject, defined } = provider(fixture);
    assert.deepEqual(await subject.provideMcpServerDefinitions(), [], JSON.stringify(fixture));
    assert.deepEqual(defined, []);
  }
  assert.equal(mcpLaunch({ enabled: false, path: defaultCommand }, { fsPath: root }, true), undefined);
});

test("a blank or unset path falls back to the contributed default", async () => {
  for (const path of [undefined, "", "   "]) {
    const { provider: subject } = provider({ settings: { enabled: true, path } });
    const [definition] = await subject.provideMcpServerDefinitions();
    assert.equal(definition?.command, defaultCommand, `path ${JSON.stringify(path)}`);
  }
});

test("a command name or relative path is left to the host without a file check", async () => {
  for (const path of ["standards-mcp", "bin/standards-mcp"]) {
    const { provider: subject, checked } = provider({ settings: { enabled: true, path }, files: [] });
    const [definition] = await subject.provideMcpServerDefinitions();
    assert.equal(definition?.command, path);
    assert.deepEqual(checked, []);
  }
});

test("Windows counts the .com and .exe of an extension-less command", async () => {
  const command = "C:\\work\\repo\\bin\\standards-mcp";
  assert.deepEqual(launchCandidates(command, "win32"), [command, `${command}.com`, `${command}.exe`]);
  assert.deepEqual(launchCandidates(`${command}.exe`, "win32"), [`${command}.exe`]);
  assert.deepEqual(launchCandidates(defaultCommand, "linux"), [defaultCommand]);
  assert.deepEqual(launchCandidates(defaultCommand, "darwin"), [defaultCommand]);
  const { provider: subject } = provider({ platform: "win32", files: [`${defaultCommand}.exe`] });
  assert.equal((await subject.provideMcpServerDefinitions()).length, 1);
  const { provider: linux } = provider({ platform: "linux", files: [`${defaultCommand}.exe`] });
  assert.deepEqual(await linux.provideMcpServerDefinitions(), []);
});

test("a standards.mcp change, trust or folder change fires the definitions event", () => {
  const { provider: subject } = provider();
  let fired = 0;
  subject.onDidChangeMcpServerDefinitions(() => { fired++; });
  subject.configurationChanged({ affectsConfiguration: section => section === "standards.mcp" });
  assert.equal(fired, 1);
  subject.configurationChanged({ affectsConfiguration: section => section === "standards.lsp" });
  assert.equal(fired, 1, "an unrelated setting fired the MCP event");
  subject.refresh();
  assert.equal(fired, 2);
});

// watching wires a LaunchFileWatch the way extension.ts does: update() after every provider change,
// with a fake WatchFile that records the watched files and their change callbacks.
function watching(subject: StandardsMcpProvider<McpLaunch>) {
  const active = new Map<string, () => void>();
  let started = 0;
  const watch = new LaunchFileWatch(subject, (file, changed) => {
    started++;
    active.set(file, changed);
    return { dispose: () => { active.delete(file); } };
  });
  subject.onDidChangeMcpServerDefinitions(() => watch.update());
  watch.update();
  return { watch, active, started: () => started };
}

test("a server built after activation is listed without a reload", async () => {
  const fixture: Fixture = { files: [] };
  const { provider: subject } = provider(fixture);
  const { active } = watching(subject);
  let fired = 0;
  subject.onDidChangeMcpServerDefinitions(() => { fired++; });
  assert.deepEqual(await subject.provideMcpServerDefinitions(), []);
  assert.deepEqual([...active.keys()], [defaultCommand]);
  // make build writes bin/standards-mcp: the watcher reports it and VS Code asks again.
  fixture.files = [defaultCommand];
  active.get(defaultCommand)?.();
  assert.equal(fired, 1);
  assert.equal((await subject.provideMcpServerDefinitions()).length, 1);
});

test("nothing is watched without a launch or for a command the host looks up", () => {
  const cases: Fixture[] = [
    { settings: { enabled: false, path: undefined } },
    { trusted: false },
    { folder: undefined },
    { settings: { enabled: true, path: "standards-mcp" } },
    { settings: { enabled: true, path: "bin/standards-mcp" } },
  ];
  for (const fixture of cases) {
    const { provider: subject } = provider(fixture);
    assert.deepEqual(subject.launchFiles(), [], JSON.stringify(fixture));
    const { active, started } = watching(subject);
    assert.equal(active.size + started(), 0, JSON.stringify(fixture));
  }
});

test("the watch covers at most the three launch candidates and follows setting changes", () => {
  const fixture: Fixture = { platform: "win32" };
  const { provider: subject } = provider(fixture);
  const { watch, active, started } = watching(subject);
  assert.deepEqual([...active.keys()], [defaultCommand, `${defaultCommand}.com`, `${defaultCommand}.exe`]);
  // A change that leaves the files alone keeps the same watchers.
  subject.refresh();
  assert.equal(started(), 3);
  // A new path replaces them: the old watchers are disposed, one new file is watched.
  const moved = `${root}/tools/praetor-mcp.exe`;
  fixture.settings = { enabled: true, path: moved };
  subject.configurationChanged({ affectsConfiguration: section => section === "standards.mcp" });
  assert.deepEqual([...active.keys()], [moved]);
  assert.equal(started(), 4);
  watch.dispose();
  assert.equal(active.size, 0);
});

test("a changed active folder re-announces the definitions, an unchanged one does not", async () => {
  // A multi-root window with no active editor at activation selects no folder.
  const fixture: Fixture = { folder: undefined };
  const { provider: subject } = provider(fixture);
  let fired = 0;
  subject.onDidChangeMcpServerDefinitions(() => { fired++; });
  assert.deepEqual(await subject.provideMcpServerDefinitions(), []);
  subject.activeFolderChanged();
  assert.equal(fired, 0, "no folder before and after fired the event");
  fixture.folder = { fsPath: root };
  subject.activeFolderChanged();
  assert.equal(fired, 1);
  assert.equal((await subject.provideMcpServerDefinitions()).length, 1);
  subject.activeFolderChanged();
  assert.equal(fired, 1, "an editor in the same folder fired the event");
  fixture.folder = { fsPath: "/work/other" };
  subject.activeFolderChanged();
  assert.equal(fired, 2);
});

const folderA = { uri: { toString: () => "file:///work/a" } };
const folderB = { uri: { toString: () => "file:///work/b" } };

test("stickyFolder positive: the active editor's root wins in a multi-root window", () => {
  assert.equal(stickyFolder(folderA, folderB, [folderA, folderB]), folderB);
  assert.equal(stickyFolder(undefined, folderA, [folderA, folderB]), folderA);
});

test("stickyFolder negative: an editor outside every root keeps the previous root", () => {
  assert.equal(stickyFolder(folderA, undefined, [folderA, folderB]), folderA);
  assert.equal(stickyFolder(undefined, undefined, [folderA, folderB]), undefined);
  assert.equal(stickyFolder(folderA, undefined, [folderB, { uri: { toString: () => "file:///work/c" } }]), undefined);
});

test("stickyFolder boundary: one folder always binds, none never does", () => {
  assert.equal(stickyFolder(undefined, undefined, [folderA]), folderA);
  assert.equal(stickyFolder(folderB, undefined, [folderA]), folderA);
  assert.equal(stickyFolder(folderA, folderA, []), undefined);
  assert.equal(stickyFolder(folderA, folderA, undefined), undefined);
});
