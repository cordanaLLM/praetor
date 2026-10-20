// localStorage that never throws: a private window, blocked site data or a full quota turns
// reads into "nothing stored" and writes into no-ops, so the console works without it.

export interface SafeStorage {
	get(key: string): string | null;
	set(key: string, value: string): void;
}

export function safeStorage(source: () => Storage | null | undefined): SafeStorage {
	return {
		get(key) {
			try {
				return source()?.getItem(key) ?? null;
			} catch {
				return null;
			}
		},
		set(key, value) {
			try {
				source()?.setItem(key, value);
			} catch {
				// Storage refused the write; the URL still carries the view.
			}
		}
	};
}

export function browserStorage(): SafeStorage {
	return safeStorage(() => (typeof window === 'undefined' ? null : window.localStorage));
}
