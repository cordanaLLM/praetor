export function killProcessGroup(child: { pid: number }): void {
  process.kill(-child.pid, "SIGKILL");
}
