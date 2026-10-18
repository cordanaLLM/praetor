export function probeGroup(pid) {
  process.kill(-pid, 0);
}
