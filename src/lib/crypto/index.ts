/**
 * Ivy Encryption Module
 *
 * Zero-knowledge encryption utilities for email content.
 * All encryption happens client-side.
 */

// TODO: Implement AES-256-GCM encrypt/decrypt
export async function encrypt(data: ArrayBuffer, key: CryptoKey): Promise<ArrayBuffer> {
	throw new Error('Not implemented');
}

export async function decrypt(data: ArrayBuffer, key: CryptoKey): Promise<ArrayBuffer> {
	throw new Error('Not implemented');
}

// TODO: Implement Argon2id key derivation
export async function deriveWrapperKey(password: string, salt: Uint8Array): Promise<CryptoKey> {
	throw new Error('Not implemented');
}

// TODO: Implement email key generation
export function generateEmailKey(): Promise<CryptoKey> {
	throw new Error('Not implemented');
}

// TODO: Implement BIP39 recovery phrase
export function generateRecoveryPhrase(): string[] {
	throw new Error('Not implemented');
}

export function recoveryPhraseToKey(phrase: string[]): Promise<CryptoKey> {
	throw new Error('Not implemented');
}
