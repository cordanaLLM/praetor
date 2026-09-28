import * as path from "node:path";

export type ClientCapability = {
  client: string; mode: "native" | "merge" | "export"; documentation: string;
  lifecycle: { state: "adapter-defined" | "unsupported"; activation: "unverified"; definition_paths: string[] };
};

// The LanguageClient id is also the configuration section vscode-languageclient reads the trace
// level from: `<id>.trace.server`, re-read on every configuration change. With this id that key is
// the contributed standards.lsp.trace.server. The former id "standardsLSP" made the client read
// standardsLSP.trace.server, a key nothing contributes, so the setting never took effect.
export const LSP_CLIENT_ID = "standards.lsp";

// Contributed defaults of standards.lsp.path and standards.mcp.path (package.json); setup.test.ts
// checks they stay equal.
export const LSP_DEFAULT_PATH = "${workspaceFolder}/bin/standards-lsp";
export const MCP_DEFAULT_PATH = "${workspaceFolder}/bin/standards-mcp";

// workspaceExecutable resolves a server path setting for one workspace folder: a value that is not
// a non-blank string falls back to the contributed default, then ${workspaceFolder} becomes the
// folder path, inserted literally (a replacer function, so `$&` in a path is not a pattern). The
// LSP and the MCP server resolve their paths through this one function.
export function workspaceExecutable(configured: unknown, fallback: string, folderPath: string): string {
  const value = typeof configured === "string" && configured.trim() ? configured : fallback;
  return value.replaceAll("${workspaceFolder}", () => folderPath);
}

// Files whose presence at a workspace folder root marks a Praetor workspace. They are the files the
// workspaceContains activation events in package.json name; setup.test.ts checks the two agree.
export const PRAETOR_MARKERS = [".standards.yaml", "AGENTS.md"] as const;
// MAX_MARKER_FOLDERS bounds the folders praetorWorkspace inspects in one multi-root window.
export const MAX_MARKER_FOLDERS = 64;

export type IsFile = (file: string) => Promise<boolean>;

// launchCandidates lists the files an absolute command can start. On Windows a command without an
// extension also names <command>.com and <command>.exe, which libuv's search_path appends
// (docs/guides/editor-capabilities.md, the same rule the generated LSP settings rely on), so a
// built bin/standards-mcp.exe counts for the default bin/standards-mcp.
export function launchCandidates(command: string, platform: NodeJS.Platform): string[] {
  if (platform !== "win32" || path.win32.extname(command) !== "") return [command];
  return [command, `${command}.com`, `${command}.exe`];
}

// commandAvailable reports whether a server command can start: an absolute command must name an
// existing file (one of its launchCandidates). A command name or relative path is left to the
// host's own lookup, unchecked. The LSP and the MCP provider both gate on it, so a workspace that
// never built bin/standards-lsp or bin/standards-mcp starts and lists nothing.
export async function commandAvailable(command: string, platform: NodeJS.Platform, isFile: IsFile): Promise<boolean> {
  const flavor = platform === "win32" ? path.win32 : path.posix;
  if (!flavor.isAbsolute(command)) return true;
  for (const candidate of launchCandidates(command, platform)) {
    if (await isFile(candidate)) return true;
  }
  return false;
}

// praetorWorkspace reports whether one of the first MAX_MARKER_FOLDERS workspace folders holds a
// PRAETOR_MARKERS file at its root. The status bar shows only then, so an activation from another
// source (a Go file, VS Code's MCP discovery) stays silent in an unrelated workspace.
export async function praetorWorkspace(folderPaths: readonly string[], isFile: IsFile): Promise<boolean> {
  for (const folder of folderPaths.slice(0, MAX_MARKER_FOLDERS)) {
    for (const marker of PRAETOR_MARKERS) {
      if (await isFile(path.join(folder, marker))) return true;
    }
  }
  return false;
}

// sentinelArguments turns standards.sentinel.headroomMB into the praetorctl sentinel call that
// measures it. Zero would skip the CLI check, so only a positive integer is passed; the CLI refuses
// values above its MiB limit.
export function sentinelArguments(headroomMB: unknown): string[] {
  if (typeof headroomMB !== "number" || !Number.isSafeInteger(headroomMB) || headroomMB < 1) {
    throw new Error("standards.sentinel.headroomMB must be a positive whole number of MiB.");
  }
  return ["sentinel", `--min-free-mb=${headroomMB}`];
}

export function requireTrust(trusted: boolean): void {
  if (!trusted) throw new Error("Workspace Trust is required before running Praetor.");
}

export function workspaceGlob(root: string): string {
  // VS Code glob patterns use bracket expressions to match literal glob symbols.
  return root.replaceAll("\\", "/").replace(/[*?\[\]{}]/g, char => `[${char}]`).replace(/\/$/, "") + "/**/*.go";
}

export function machineExecutable(globalValue: unknown, defaultValue: string): string {
  const executable = globalValue === undefined ? defaultValue : globalValue;
  if (typeof executable !== "string" || !executable.trim() || executable.includes("\0")) {
    throw new Error("Configure a machine-level executable path.");
  }
  if (!path.isAbsolute(executable) && !/^[A-Za-z0-9_.-]+$/.test(executable)) {
    throw new Error("Executable must be an absolute path or a PATH command name.");
  }
  return executable;
}

export function parseCapabilities(raw: string): ClientCapability[] {
  const report: unknown = JSON.parse(raw);
  if (!isObject(report) || report.schema_version !== 1 || report.runtime_verified !== false || !Array.isArray(report.clients)) {
    throw new Error("Unsupported or overclaimed client capability report.");
  }
  if (report.clients.length < 1 || report.clients.length > 64) throw new Error("Client inventory requires 1..64 entries.");
  const seen = new Set<string>();
  return report.clients.map((value: unknown) => {
    const item = validateCapability(value);
    if (seen.has(item.client)) throw new Error("Duplicate client capability.");
    seen.add(item.client);
    return item;
  });
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function validateCapability(value: unknown): ClientCapability {
  if (!isObject(value) || typeof value.client !== "string" || !/^[a-z0-9-]{1,64}$/.test(value.client) ||
      typeof value.mode !== "string" || !["native", "merge", "export"].includes(value.mode) || typeof value.documentation !== "string") {
    throw new Error("Invalid client capability entry.");
  }
  const lifecycle = value.lifecycle;
  if (!isObject(lifecycle) || lifecycle.activation !== "unverified" ||
      typeof lifecycle.state !== "string" || !["adapter-defined", "unsupported"].includes(lifecycle.state) || !Array.isArray(lifecycle.definition_paths) ||
      !lifecycle.definition_paths.every((entry: unknown) => typeof entry === "string")) {
    throw new Error("Invalid lifecycle capability; definitions do not establish activation.");
  }
  return value as unknown as ClientCapability;
}

export function artifactPath(parent: string, name: string): string {
  if (!path.isAbsolute(parent) || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(name)) {
    throw new Error("Choose an absolute parent and a new artifact directory name.");
  }
  return path.join(parent, name);
}

export function setupArguments(client: ClientCapability, apply: boolean, registry: string, output: string, config?: string): string[] {
  if (apply && client.mode === "native") throw new Error("This client requires its native configuration workflow.");
  for (const file of [registry, output, ...(config ? [config] : [])]) {
    if (!path.isAbsolute(file) || file.includes("\0")) throw new Error("Setup requires explicit absolute paths.");
  }
  if (apply && !config) throw new Error("Apply requires an explicit target.");
  if (client.mode === "native" && config) throw new Error("Native preparation cannot merge an existing configuration.");
  const args = ["clients", apply ? "apply" : "prepare", "--registry", registry, "--client", client.client, "--out", output];
  if (config) args.push(apply ? "--target" : "--existing", config);
  return args;
}
