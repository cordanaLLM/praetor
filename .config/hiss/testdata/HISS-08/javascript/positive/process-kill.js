export function killProcessGroup(child) {
  process.kill(-child.pid, "SIGKILL");
}
