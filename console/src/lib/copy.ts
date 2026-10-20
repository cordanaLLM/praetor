// Copy text exactly as given. The async Clipboard API first; where it is missing or refused
// (an insecure origin, a denied permission), a hidden textarea and execCommand('copy').

export type CopyOutcome = 'copied' | 'failed';

function legacyCopy(text: string, doc: Document): boolean {
	const area = doc.createElement('textarea');
	area.value = text;
	area.setAttribute('readonly', '');
	area.style.position = 'fixed';
	area.style.opacity = '0';
	doc.body.appendChild(area);
	area.select();
	try {
		return doc.execCommand('copy');
	} catch {
		return false;
	} finally {
		area.remove();
	}
}

export async function copyText(
	text: string,
	clipboard: Pick<Clipboard, 'writeText'> | undefined = globalThis.navigator?.clipboard,
	doc: Document | undefined = globalThis.document
): Promise<CopyOutcome> {
	if (clipboard) {
		try {
			await clipboard.writeText(text);
			return 'copied';
		} catch {
			// The asynchronous clipboard refused (permission, insecure context): the legacy path decides.
			return legacyOutcome(text, doc);
		}
	}
	return legacyOutcome(text, doc);
}

function legacyOutcome(text: string, doc: Document | undefined): CopyOutcome {
	return doc && legacyCopy(text, doc) ? 'copied' : 'failed';
}
