// Signatures are plain text appended after the body behind the standard `-- `
// separator (round 63). The server already applies the reply identity's signature
// in a prefill; these helpers do the same for a new message and swap it when the
// From picker changes, so the whole text renders as one markdown document.

/** The block a signature contributes, or an empty string when there is none. */
export function signatureBlock(signature: string): string {
	return signature.trim() ? `-- \n${signature.trimEnd()}` : '';
}

/**
 * Replace the old identity's trailing signature with the new one. A body that did
 * not end with the old signature gets the new one appended; one already ending in
 * the new signature is left alone.
 */
export function applySignature(text: string, oldSignature: string, newSignature: string): string {
	const oldBlock = signatureBlock(oldSignature);
	const newBlock = signatureBlock(newSignature);
	if (oldBlock && text.endsWith(oldBlock)) {
		const head = text.slice(0, -oldBlock.length).replace(/\n+$/, '');
		if (!newBlock) return head;
		return head ? `${head}\n\n${newBlock}` : newBlock;
	}
	if (!newBlock || text.endsWith(newBlock)) return text;
	return text ? `${text}\n\n${newBlock}` : newBlock;
}
