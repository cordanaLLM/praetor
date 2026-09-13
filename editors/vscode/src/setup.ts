import * as path from "node:path";

export type ClientCapability = {
  client: string; mode: "native" | "merge" | "export"; documentation: string;
  lifecycle: { state: "adapter-defined" | "unsupported"; activation: "unverified"; definition_paths: string[] };
};

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
