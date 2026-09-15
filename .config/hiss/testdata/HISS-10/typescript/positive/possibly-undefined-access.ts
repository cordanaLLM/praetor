// Under strictNullChecks the value may be undefined and is dereferenced anyway.
export function labelLength(label: string | undefined): number {
  return label.length;
}
