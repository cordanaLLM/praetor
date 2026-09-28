import type { Event } from "vscode";
import { commandAvailable, IsFile, MCP_DEFAULT_PATH, workspaceExecutable } from "./setup";

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

  constructor(private readonly host: McpProviderHost<D, F>, private readonly emitter: ChangeEmitter) {
    this.onDidChangeMcpServerDefinitions = emitter.event;
  }

  async provideMcpServerDefinitions(): Promise<D[]> {
    const folder = this.host.folder();
    const launch = folder && mcpLaunch(this.host.settings(folder), folder, this.host.trusted());
    if (!launch || !(await commandAvailable(launch.command, this.host.platform, file => this.host.isFile(file)))) return [];
    return [this.host.define(launch)];
  }

  // configurationChanged re-announces the definitions when a standards.mcp.* setting changes.
  configurationChanged(change: { affectsConfiguration(section: string): boolean }): void {
    if (change.affectsConfiguration("standards.mcp")) this.emitter.fire();
  }

  // refresh re-announces the definitions after trust or the workspace folders change.
  refresh(): void { this.emitter.fire(); }
}
