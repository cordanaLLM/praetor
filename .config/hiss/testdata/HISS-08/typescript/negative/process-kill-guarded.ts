export function killProcessGroup(child: { pid: number }): void {
  if (child.pid <= 1) {
    return;
  }
  process.kill(-child.pid, "SIGKILL");
}
