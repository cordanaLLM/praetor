// Theme choice (light, dark, or follow the system), stored per viewer. The mode logic is
// @sveltesentio/ui's; this applies it to <html> and keeps the choice in local storage.
import {
	htmlClassFor,
	isThemeMode,
	resolveMode,
	type ThemeMode
} from '@sveltesentio/ui/theme-toggle';
import type { SafeStorage } from './view/storage.ts';

export const THEME_KEY = 'praetor-console:theme';

export function storedMode(storage: SafeStorage): ThemeMode {
	const raw = storage.get(THEME_KEY);
	return isThemeMode(raw) ? raw : 'system';
}

export function systemPrefersDark(): boolean {
	return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

export function applyMode(mode: ThemeMode, storage: SafeStorage, root: HTMLElement): void {
	storage.set(THEME_KEY, mode);
	const dark = htmlClassFor(resolveMode(mode, systemPrefersDark())) === 'dark';
	root.classList.toggle('dark', dark);
}
