export function probeGroup(pid: number): void {
  process.kill(-pid, 0);
}
