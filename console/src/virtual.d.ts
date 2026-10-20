declare module 'virtual:console-validators' {
	interface CompiledError {
		instancePath: string;
		keyword: string;
		message?: string;
	}
	interface CompiledValidator {
		(value: unknown): boolean;
		errors?: CompiledError[] | null;
	}
	export const cards: CompiledValidator;
	export const graph: CompiledValidator;
	export const status: CompiledValidator;
}

interface ImportMetaEnv {
	/** Server-sent events endpoint announcing new data; empty means timed refetch. */
	readonly VITE_CONSOLE_EVENTS_URL?: string;
	/** Seconds between refetches when no event stream is configured (default 60). */
	readonly VITE_CONSOLE_REFRESH_SECONDS?: string;
}
