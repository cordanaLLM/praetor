import { stat } from "node:fs/promises";
import * as path from "node:path";
import * as vscode from "vscode";
import { LanguageClient } from "vscode-languageclient/node";
import { LaunchFileWatch, MCP_PROVIDER_ID, McpLaunch, StandardsMcpProvider, stickyFolder } from "./mcp";
import { DEFAULT_TIMEOUT_MS, runCLI } from "./runner";
import { artifactPath, boundedIsFile, ClientCapability, commandAvailable, globLiteral, LSP_CLIENT_ID, LSP_DEFAULT_PATH, machineExecutable, parseCapabilities, praetorWorkspace, requireTrust, sentinelArguments, setupArguments, workspaceExecutable, workspaceGlob } from "./setup";

let client: LanguageClient | undefined;
let lspStarting = false;
let output: vscode.OutputChannel;

// Every existence probe is raced against FILE_PROBE_TIMEOUT_MS; a stalled mount counts as absent.
const isFile = boundedIsFile(file => stat(file).then(entry => entry.isFile()));

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  output = vscode.window.createOutputChannel("Praetor");
  context.subscriptions.push(output);
  registerCommands(context);
  setupStatusBar(context);
  const mcp = registerMcpProvider(context);
  context.subscriptions.push(vscode.workspace.onDidGrantWorkspaceTrust(() => { mcp?.refresh(); void startOptionalLSP(context); }));
  await startOptionalLSP(context);
}

// selectedFolder is the last folder serverFolder chose; stickyFolder keeps it while the active
// editor belongs to no workspace folder.
let selectedFolder: vscode.WorkspaceFolder | undefined;

// serverFolder is the folder the LSP and the MCP server bind to: the only workspace folder, or
// the active editor's folder in a multi-root window, never an arbitrary first folder, and the
// previous choice while the active editor belongs to none (stickyFolder).
function serverFolder(): vscode.WorkspaceFolder | undefined {
  const active = vscode.window.activeTextEditor && vscode.workspace.getWorkspaceFolder(vscode.window.activeTextEditor.document.uri);
  selectedFolder = stickyFolder(selectedFolder, active, vscode.workspace.workspaceFolders);
  return selectedFolder;
}

function registerMcpProvider(context: vscode.ExtensionContext): StandardsMcpProvider<vscode.McpStdioServerDefinition, vscode.Uri> | undefined {
  // An editor that satisfies engines.vscode without this API keeps the commands and the LSP.
  if (typeof vscode.lm?.registerMcpServerDefinitionProvider !== "function") return undefined;
  const emitter = new vscode.EventEmitter<void>();
  const provider = new StandardsMcpProvider({
    trusted: () => vscode.workspace.isTrusted,
    folder: () => serverFolder()?.uri,
    settings: (folder: vscode.Uri) => {
      const config = vscode.workspace.getConfiguration("standards", folder);
      return { enabled: config.get<boolean>("mcp.enabled", true), path: config.get<string>("mcp.path") };
    },
    isFile,
    platform: process.platform,
    define: (launch: McpLaunch) => {
      const server = new vscode.McpStdioServerDefinition(launch.label, launch.command, launch.args);
      server.cwd = vscode.Uri.file(launch.cwd);
      return server;
    },
  }, emitter);
  context.subscriptions.push(emitter, vscode.lm.registerMcpServerDefinitionProvider(MCP_PROVIDER_ID, provider),
    vscode.workspace.onDidChangeConfiguration(change => provider.configurationChanged(change)),
    vscode.workspace.onDidChangeWorkspaceFolders(() => provider.refresh()),
    vscode.window.onDidChangeActiveTextEditor(() => provider.activeFolderChanged()));
  watchLaunchFiles(context, provider);
  return provider;
}

// watchLaunchFiles refreshes the provider when a launch file of the current server appears or
// disappears, so the first build after activation lists the server without a reload. A watch on a
// directory that does not exist yet starts once it is created (workspace.createFileSystemWatcher).
function watchLaunchFiles(context: vscode.ExtensionContext, provider: StandardsMcpProvider<vscode.McpStdioServerDefinition, vscode.Uri>): void {
  const watch = new LaunchFileWatch(provider, (file, changed) => {
    const pattern = new vscode.RelativePattern(vscode.Uri.file(path.dirname(file)), globLiteral(path.basename(file)));
    const watcher = vscode.workspace.createFileSystemWatcher(pattern, false, true, false);
    watcher.onDidCreate(changed);
    watcher.onDidDelete(changed);
    return watcher;
  });
  context.subscriptions.push(watch, provider.onDidChangeMcpServerDefinitions(() => watch.update()));
  watch.update();
}

async function selectWorkspaceFolder(): Promise<vscode.WorkspaceFolder | undefined> {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders?.length) throw new Error("Open a workspace folder before running Praetor.");
  if (folders.length === 1) return folders[0];
  return vscode.window.showWorkspaceFolderPick({ placeHolder: "Select the workspace for this Praetor operation" });
}

// lspExecutable resolves the LSP command for a folder, or nothing when standards.lsp.enabled is
// false or an absolute path names no file. A missing binary is logged to the Praetor channel, not
// raised: VS Code also activates the extension for Go files and MCP discovery in workspaces that
// never built bin/standards-lsp.
async function lspExecutable(folder: vscode.WorkspaceFolder): Promise<string | undefined> {
  const config = vscode.workspace.getConfiguration("standards", folder.uri);
  if (!config.get<boolean>("lsp.enabled", true)) return undefined;
  const executable = workspaceExecutable(config.get<string>("lsp.path"), LSP_DEFAULT_PATH, folder.uri.fsPath);
  if (await commandAvailable(executable, process.platform, isFile)) return executable;
  output.appendLine(`Praetor LSP not started: ${executable} does not exist. Build it or set standards.lsp.path.`);
  return undefined;
}

async function startOptionalLSP(context: vscode.ExtensionContext): Promise<void> {
  const folder = serverFolder();
  if (!vscode.workspace.isTrusted || client || lspStarting || !folder) return;
  lspStarting = true;
  let executable: string | undefined;
  try { executable = await lspExecutable(folder); } finally { lspStarting = false; }
  if (!executable || client) return;
  const pattern = new vscode.RelativePattern(folder, "**/*.go");
  const watcher = vscode.workspace.createFileSystemWatcher(pattern);
  context.subscriptions.push(watcher);
  client = new LanguageClient(LSP_CLIENT_ID, "Praetor LSP", { command: executable, args: [], options: { cwd: folder.uri.fsPath } }, {
    documentSelector: [{ scheme: "file", language: "go", pattern: workspaceGlob(folder.uri.fsPath) }], workspaceFolder: folder, synchronize: { fileEvents: watcher },
  });
  context.subscriptions.push(client);
  try { await client.start(); } catch (error) { void vscode.window.showWarningMessage(`Praetor LSP unavailable: ${String(error)}`); }
}

// setupStatusBar shows the status item only while a workspace folder holds a Praetor marker file.
function setupStatusBar(context: vscode.ExtensionContext): void {
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
  status.text = "$(shield) Praetor: Unverified";
  status.tooltip = "Configuration and lifecycle enforcement need separate checks. Open agent setup.";
  status.command = "standards.setupAgents";
  const update = async (): Promise<void> => {
    const folders = (vscode.workspace.workspaceFolders ?? []).map(folder => folder.uri.fsPath);
    if (await praetorWorkspace(folders, isFile)) status.show(); else status.hide();
  };
  context.subscriptions.push(status, vscode.workspace.onDidChangeWorkspaceFolders(() => void update()));
  void update();
}

function registerCommands(context: vscode.ExtensionContext): void {
  const register = (id: string, action: (folder: vscode.WorkspaceFolder) => Promise<void>) => {
    context.subscriptions.push(vscode.commands.registerCommand(id, () => withWorkspace(action)));
  };
  register("standards.audit", async folder => { await execute(folder, cliPath(), ["audit"], "Praetor audit"); output.show(true); });
  register("standards.compileContext", async folder => { await execute(folder, cliPath(), ["compile-context"], "Compile agent context"); output.show(true); });
  register("standards.runGC", async folder => {
    const seconds = vscode.workspace.getConfiguration("standards").get<number>("verificationTimeoutSeconds", 300);
    if (!Number.isInteger(seconds) || seconds < 10 || seconds > 600) throw new Error("Verification timeout must be 10..600 seconds.");
    await execute(folder, "make", ["verify-all"], "Praetor verification", seconds * 1000);
    output.show(true);
  });
  register("standards.setupAgents", setupAgents);
  register("standards.checkSentinel", async folder => {
    const headroom = vscode.workspace.getConfiguration("standards", folder.uri).get<number>("sentinel.headroomMB", 1024);
    // praetorctl sentinel measures free RAM and exits non-zero below the headroom or without a reading.
    await execute(folder, cliPath(), sentinelArguments(headroom), "Check sentinel headroom");
    output.show(true);
    void vscode.window.showInformationMessage(`Measured free RAM meets the ${headroom} MiB headroom. See the Praetor output for the full sentinel report.`);
  });
}

async function withWorkspace(action: (folder: vscode.WorkspaceFolder) => Promise<void>): Promise<void> {
  try {
    requireTrust(vscode.workspace.isTrusted);
    const folder = await selectWorkspaceFolder();
    if (folder) await action(folder);
  } catch (error) { void vscode.window.showErrorMessage(`Praetor: ${String(error)}`); }
}

function cliPath(): string {
  // Read machine/user authority explicitly; never take workspace executable overrides.
  const setting = vscode.workspace.getConfiguration("standards").inspect<string>("cli.path");
  return machineExecutable(setting?.globalValue, setting?.defaultValue ?? "praetorctl");
}

async function execute(folder: vscode.WorkspaceFolder, executable: string, args: string[], title: string, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<string> {
  requireTrust(vscode.workspace.isTrusted);
  return vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title, cancellable: true }, async (_progress, token) => {
    const controller = new AbortController();
    const listener = token.onCancellationRequested(() => controller.abort());
    if (token.isCancellationRequested) controller.abort();
    try {
      const result = await runCLI(executable, args, folder.uri.fsPath, { signal: controller.signal, timeoutMs });
      output.appendLine(`${title} (exit ${result.exitCode})`);
      output.append(result.stdout);
      output.append(result.stderr);
      if (result.timedOut || result.cancelled || result.outputExceeded || result.exitCode !== 0) {
        output.show(true);
        const reason = result.cancelled ? "cancelled" : result.timedOut ? "timed out" :
          result.outputExceeded ? "output exceeded 1 MiB" : result.invalidOutput ? "invalid UTF-8 output" : `exit ${result.exitCode}`;
        throw new Error(`${title} incomplete: ${reason}. Inspect Praetor output and retained artifacts before retrying.`);
      }
      return result.stdout;
    } finally { listener.dispose(); }
  });
}

async function setupAgents(folder: vscode.WorkspaceFolder): Promise<void> {
  const cli = cliPath();
  const clients = parseCapabilities(await execute(folder, cli, ["clients", "capabilities"], "Read client capabilities"));
  const selected = await vscode.window.showQuickPick(clients.map(item => ({ label: item.client, description: `${item.mode}; lifecycle ${item.lifecycle.state}`, item })), { placeHolder: "Select a configured client type; native activation is unverified" });
  if (!selected) return;
  const item = selected.item;
  const choices = item.mode === "native" ? ["Prepare MCP configuration"] : ["Prepare MCP configuration", "Apply MCP configuration"];
  const operation = await vscode.window.showQuickPick(choices, { placeHolder: "Choose the MCP configuration stage; wrapper installation is separate" });
  if (!operation) return;
  const apply = operation === "Apply MCP configuration";
  const registry = await selectFile("Select the shared MCP registry");
  if (!registry) return;
  const config = await selectConfiguration(item, apply);
  if (config === null) return;
  const destination = await selectArtifactDirectory(folder);
  if (!destination) return;
  const args = setupArguments(item, apply, registry, destination, config);
  await execute(folder, cli, args, operation);
  void vscode.window.showInformationMessage(`${operation} completed for ${item.client}. Wrappers, native trust, tool use and lifecycle enforcement remain unverified.`);
}

async function selectFile(label: string): Promise<string | undefined> {
  const files = await vscode.window.showOpenDialog({ canSelectFiles: true, canSelectFolders: false, canSelectMany: false, openLabel: label });
  return files?.[0]?.fsPath;
}

async function selectConfiguration(item: ClientCapability, apply: boolean): Promise<string | undefined | null> {
  if (item.mode === "native") return undefined;
  if (apply) {
    const uri = await vscode.window.showSaveDialog({ title: "Select the exact client configuration target", saveLabel: "Use target" });
    return uri?.fsPath ?? null;
  }
  const choice = await vscode.window.showQuickPick(["Merge existing configuration", "Prepare new configuration"], { placeHolder: "Preserve an existing configuration or explicitly prepare a new one" });
  if (!choice) return null;
  if (choice === "Prepare new configuration") return undefined;
  return await selectFile("Select existing client configuration") ?? null;
}

async function selectArtifactDirectory(folder: vscode.WorkspaceFolder): Promise<string | undefined> {
  const parents = await vscode.window.showOpenDialog({ canSelectFiles: false, canSelectFolders: true, canSelectMany: false, defaultUri: folder.uri, openLabel: "Select existing private parent for artifacts" });
  if (!parents?.[0]) return undefined;
  const name = await vscode.window.showInputBox({ prompt: "Name a NEW artifact directory within this parent", value: "praetor-client-setup", validateInput: value => /^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(value) ? undefined : "Use 1–100 letters, digits, dots, underscores or hyphens; start with a letter or digit." });
  return name ? artifactPath(parents[0].fsPath, name) : undefined;
}

export function deactivate(): Thenable<void> | undefined { return client?.stop(); }
