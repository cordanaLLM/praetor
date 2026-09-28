import type { Event } from "vscode";
import { checkedLaunchFiles, commandAvailable, IsFile, MCP_DEFAULT_PATH, workspaceExecutable } from "./setup";

// The id contributed under contributes.mcpServerDefinitionProviders in package.json. VS Code
// only accepts registerMcpServerDefinitionProvider for an id the extension contributes.
export const MCP_PROVIDER_ID = "standards.mcp";
export const MCP_SERVER_LABEL = "Praetor standards-mcp";

export type McpSettings = { enabled: boolean; path: string | undefined };
export type McpFolder = { fsPath: string };
export type McpLaunch = { label: string; command: string; args: string[]; cwd: string };

// mcpLaunch describes the one standards-mcp server for a folder, or nothing when the workspace is
// untrusted, no folder is selected or standards.mcp.enabled is false. The server gets an explicit
// -root, so its path confinement does not depend on the spawn directory.
export function mcpLaunch(settings: McpSettings, folder: McpFolder | undefined, trusted: boolean): McpLaunch | undefined {
  if (!trusted || !folder || !settings.enabled) return undefined;
  const command = workspaceExecutable(settings.path, MCP_DEFAULT_PATH, folder.fsPath);
  return { label: MCP_SERVER_LABEL, command, args: ["-transport=stdio", "-root", folder.fsPath], cwd: folder.fsPath };
}

export interface McpProviderHost<D, F extends McpFolder = McpFolder> {
  trusted(): boolean;
  folder(): F | undefined;
  settings(folder: F): McpSettings;
  isFile: IsFile;
  platform: NodeJS.Platform;
  define(launch: McpLaunch): D;
}

export interface ChangeEmitter { readonly event: Event<void>; fire(): void }

// StandardsMcpProvider is the McpServerDefinitionProvider the extension registers. It offers the
// server only when commandAvailable (src/setup.ts) holds, so a workspace that never built
// bin/standards-mcp does not list a server that cannot start.
export class StandardsMcpProvider<D, F extends McpFolder = McpFolder> {
  readonly onDidChangeMcpServerDefinitions: Event<void>;
  // The folder path the last provideMcpServerDefinitions answer was computed for.
  private answeredFolder: string | undefined;

  constructor(private readonly host: McpProviderHost<D, F>, private readonly emitter: ChangeEmitter) {
    this.onDidChangeMcpServerDefinitions = emitter.event;
  }

  async provideMcpServerDefinitions(): Promise<D[]> {
    const folder = this.host.folder();
    this.answeredFolder = folder?.fsPath;
    const launch = this.launchFor(folder);
    if (!launch || !(await commandAvailable(launch.command, this.host.platform, file => this.host.isFile(file)))) return [];
    return [this.host.define(launch)];
  }

  // launchFiles lists the files whose creation or deletion changes the answer: the
  // checkedLaunchFiles of the current launch, at most three, and none without a launch or for a
  // command name the host looks up itself.
  launchFiles(): string[] {
    const launch = this.launchFor(this.host.folder());
    return launch ? checkedLaunchFiles(launch.command, this.host.platform) : [];
  }

  // activeFolderChanged re-announces the definitions when the selected folder differs from the one
  // the last answer used: in a multi-root window the active editor picks the folder, so opening the
  // first editor after activation, or switching to another root, changes the server.
  activeFolderChanged(): void {
    if (this.host.folder()?.fsPath !== this.answeredFolder) this.emitter.fire();
  }

  private launchFor(folder: F | undefined): McpLaunch | undefined {
    return folder && mcpLaunch(this.host.settings(folder), folder, this.host.trusted());
  }

  // configurationChanged re-announces the definitions when a standards.mcp.* setting changes.
  configurationChanged(change: { affectsConfiguration(section: string): boolean }): void {
    if (change.affectsConfiguration("standards.mcp")) this.emitter.fire();
  }

  // refresh re-announces the definitions after trust or the workspace folders change, or when a
  // launch file is created or deleted (LaunchFileWatch).
  refresh(): void { this.emitter.fire(); }
}

export interface Disposable { dispose(): void }

// WatchFile starts watching one file for creation and deletion, calling changed on either.
export type WatchFile = (file: string, changed: () => void) => Disposable;

// LaunchFileWatch keeps one watcher per provider.launchFiles() entry, at most three, and refreshes
// the provider when one of them is created or deleted. Without it the first build after
// activation (make build writing bin/standards-mcp) listed no server until a reload, because VS
// Code asks the provider again only when it fires. update() re-reads the files after every
// provider change and replaces the watchers only when the list differs.
export class LaunchFileWatch<D, F extends McpFolder = McpFolder> implements Disposable {
  private watched = "";
  private watchers: Disposable[] = [];

  constructor(private readonly provider: StandardsMcpProvider<D, F>, private readonly watch: WatchFile) {}

  update(): void {
    const files = this.provider.launchFiles();
    const key = files.join("\0");
    if (key === this.watched) return;
    this.dispose();
    this.watched = key;
    this.watchers = files.map(file => this.watch(file, () => this.provider.refresh()));
  }

  dispose(): void {
    for (const watcher of this.watchers) watcher.dispose();
    this.watchers = [];
    this.watched = "";
  }
}
