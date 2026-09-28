import * as path from "node:path";
import type { Event } from "vscode";
import { MCP_DEFAULT_PATH, workspaceExecutable } from "./setup";

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

// launchCandidates lists the files an absolute command can start. On Windows a command without an
// extension also names <command>.com and <command>.exe, which libuv's search_path appends
// (docs/guides/editor-capabilities.md, the same rule the generated LSP settings rely on), so a
// built bin/standards-mcp.exe counts for the default bin/standards-mcp.
export function launchCandidates(command: string, platform: NodeJS.Platform): string[] {
  if (platform !== "win32" || path.win32.extname(command) !== "") return [command];
  return [command, `${command}.com`, `${command}.exe`];
}

export interface McpProviderHost<D, F extends McpFolder = McpFolder> {
  trusted(): boolean;
  folder(): F | undefined;
  settings(folder: F): McpSettings;
  isFile(file: string): Promise<boolean>;
  platform: NodeJS.Platform;
  define(launch: McpLaunch): D;
}

export interface ChangeEmitter { readonly event: Event<void>; fire(): void }

// StandardsMcpProvider is the McpServerDefinitionProvider the extension registers. It offers the
// server only when an absolute command exists, so a workspace that never built bin/standards-mcp
// does not list a server that cannot start. A command name or relative path is left to the host.
export class StandardsMcpProvider<D, F extends McpFolder = McpFolder> {
  readonly onDidChangeMcpServerDefinitions: Event<void>;

  constructor(private readonly host: McpProviderHost<D, F>, private readonly emitter: ChangeEmitter) {
    this.onDidChangeMcpServerDefinitions = emitter.event;
  }

  async provideMcpServerDefinitions(): Promise<D[]> {
    const folder = this.host.folder();
    const launch = folder && mcpLaunch(this.host.settings(folder), folder, this.host.trusted());
    if (!launch) return [];
    if (path.isAbsolute(launch.command) && !(await this.anyFile(launchCandidates(launch.command, this.host.platform)))) return [];
    return [this.host.define(launch)];
  }

  // configurationChanged re-announces the definitions when a standards.mcp.* setting changes.
  configurationChanged(change: { affectsConfiguration(section: string): boolean }): void {
    if (change.affectsConfiguration("standards.mcp")) this.emitter.fire();
  }

  // refresh re-announces the definitions after trust or the workspace folders change.
  refresh(): void { this.emitter.fire(); }

  private async anyFile(candidates: string[]): Promise<boolean> {
    for (const candidate of candidates) {
      if (await this.host.isFile(candidate)) return true;
    }
    return false;
  }
}
