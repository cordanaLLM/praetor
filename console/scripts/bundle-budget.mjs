// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';
import ts from 'typescript';

const { JSDOM } = createRequire(import.meta.url)('jsdom');
export const LIMIT = 153600;
const ORIGIN = 'https://bundle.invalid/';
const MAX_MODULES = 1024;

/** @param {import('typescript').Node} node @param {boolean} dynamic */
function importReference(node, dynamic) {
	if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
		const specifier = node.moduleSpecifier;
		return specifier && ts.isStringLiteral(specifier) ? specifier.text : undefined;
	}
	if (
		!dynamic ||
		!ts.isCallExpression(node) ||
		node.expression.kind !== ts.SyntaxKind.ImportKeyword
	)
		return;
	const arg = node.arguments[0];
	if (!arg || !ts.isStringLiteral(arg)) throw new Error('Non-literal entry import');
	return arg.text;
}

/** @param {string} source @param {boolean} dynamic */
function imports(source, dynamic) {
	const file = ts.createSourceFile('bundle.js', source, ts.ScriptTarget.Latest, true);
	/** @type {import('typescript').Node[]} */
	const queue = [file];
	const found = [];
	for (let i = 0; i < queue.length; i++) {
		const node = queue[i];
		if (!node) continue;
		const ref = importReference(node, dynamic);
		if (ref) found.push(ref);
		node.forEachChild((child) => {
			queue.push(child);
		});
	}
	return found;
}

/** @param {string} ref @param {string} base */
function localUrl(ref, base) {
	const url = new URL(ref, base);
	if (url.origin !== new URL(ORIGIN).origin) throw new Error(`External JavaScript: ${ref}`);
	return url;
}

/** @param {string} directory */
export async function measure(directory) {
	const root = path.resolve(directory);
	const html = await readFile(path.join(root, 'index.html'), 'utf8');
	const dom = new JSDOM(html);
	/** @type {Document} */
	const document = dom.window.document;
	const queue = new Map();
	let bytes = 0;
	for (const link of document.querySelectorAll('link[rel~="modulepreload"]')) {
		const url = localUrl(link.getAttribute('href') ?? '', ORIGIN);
		queue.set(url.href, url);
	}
	bytes += entryScripts(document, queue);
	dom.window.close();
	if (queue.size === 0) throw new Error('No initial JavaScript found');
	const modules = await measureModules(root, queue);
	return { bytes: bytes + modules.bytes, files: modules.files };
}

/** @param {Document} document @param {Map<string, URL>} queue */
function entryScripts(document, queue) {
	let bytes = 0;
	for (const script of document.querySelectorAll('script')) {
		if (
			script.type &&
			!['module', 'text/javascript', 'application/javascript'].includes(script.type)
		)
			continue;
		if (script.src) {
			const url = localUrl(script.getAttribute('src') ?? '', ORIGIN);
			queue.set(url.href, url);
		} else {
			const source = script.textContent ?? '';
			bytes += gzipSync(source, { level: 9 }).length;
			for (const ref of imports(source, true)) {
				const url = localUrl(ref, ORIGIN);
				queue.set(url.href, url);
			}
		}
	}
	return bytes;
}

/** @param {string} root @param {Map<string, URL>} queue */
async function measureModules(root, queue) {
	let bytes = 0;
	const files = [];
	for (const url of queue.values()) {
		if (files.length >= MAX_MODULES) throw new Error('Initial module limit exceeded');
		const file = path.resolve(root, `.${decodeURIComponent(url.pathname)}`);
		if (!file.startsWith(root + path.sep)) throw new Error(`Module outside build: ${url}`);
		const source = await readFile(file, 'utf8');
		bytes += gzipSync(source, { level: 9 }).length;
		files.push(path.relative(root, file));
		for (const ref of imports(source, false)) {
			const dependency = localUrl(ref, url.href);
			queue.set(dependency.href, dependency);
		}
	}
	return { bytes, files };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
	const result = await measure(process.argv[2] ?? 'build');
	process.stdout.write(
		`Initial JavaScript: ${result.bytes} bytes gzip (level 9); limit ${LIMIT}; ${result.files.length} modules\n`
	);
	if (result.bytes > LIMIT) {
		process.stderr.write('Bundle budget exceeded\n');
		process.exitCode = 1;
	}
}
