import { fireEvent, render, screen } from '@testing-library/svelte';
import { expect, test, vi } from 'vitest';
import CopyButton from './components/CopyButton.svelte';
import { copyText } from './copy.ts';

const command = "bash /opt/example/step-1.sh --dry-run  'two  spaces'\n";

test('the clipboard receives the command exactly, whitespace included', async () => {
	const writeText = vi.fn(async () => {});
	expect(await copyText(command, { writeText }, document)).toBe('copied');
	expect(writeText).toHaveBeenCalledWith(command);
});

test('a refused clipboard falls back to the legacy copy', async () => {
	const writeText = vi.fn(async () => {
		throw new DOMException('denied', 'NotAllowedError');
	});
	let copied = '';
	document.execCommand = vi.fn(() => {
		copied = document.querySelector('textarea')?.value ?? '';
		return true;
	});
	expect(await copyText(command, { writeText }, document)).toBe('copied');
	expect(copied).toBe(command);
	expect(document.querySelector('textarea')).toBeNull();
});

test('with no way to copy, the outcome is failed', async () => {
	document.execCommand = vi.fn(() => false);
	expect(await copyText(command, undefined, document)).toBe('failed');
	expect(await copyText(command, undefined, undefined)).toBe('failed');
});

test('the copy button copies its text and announces the result', async () => {
	const copy = vi.fn(async () => 'copied' as const);
	render(CopyButton, { text: command, label: 'Copy command for C1', copy });
	await fireEvent.click(screen.getByRole('button', { name: 'Copy command for C1' }));
	expect(copy).toHaveBeenCalledWith(command);
	expect(await screen.findByText('Copied')).toBeTruthy();
});

test('the copy button says when copying failed', async () => {
	render(CopyButton, {
		text: '',
		label: 'Copy command for C2',
		copy: async () => 'failed' as const
	});
	await fireEvent.click(screen.getByRole('button', { name: 'Copy command for C2' }));
	expect(await screen.findByText(/Copy failed/)).toBeTruthy();
});
