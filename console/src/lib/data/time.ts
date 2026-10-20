// Time formatting for "generated at" and "last good data" labels: UTC, unambiguous.

export function formatUtc(value: string | number | undefined): string {
	if (value === undefined) return 'never';
	const d = new Date(value);
	if (Number.isNaN(d.getTime())) return String(value);
	return `${d.toISOString().slice(0, 16).replace('T', ' ')} UTC`;
}

/** Whole minutes between two instants, never negative. */
export function minutesBetween(from: Date, to: Date): number {
	return Math.max(0, Math.floor((to.getTime() - from.getTime()) / 60_000));
}
