export function killProcessGroup(child) {
  if (child.pid <= 1) {
    return;
  }
  process.kill(-child.pid, "SIGKILL");
}
