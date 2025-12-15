/**
 * Vitest Test Setup
 *
 * Global test configuration and mocks for the Ivy test suite.
 */

import { vi } from 'vitest';

// Mock SvelteKit modules
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	invalidate: vi.fn(),
	invalidateAll: vi.fn(),
	preloadData: vi.fn(),
	preloadCode: vi.fn()
}));

vi.mock('$app/stores', () => {
	const { readable, writable } = require('svelte/store');
	return {
		page: readable({
			url: new URL('http://localhost'),
			params: {},
			route: { id: '' },
			status: 200,
			error: null,
			data: {},
			form: null
		}),
		navigating: readable(null),
		updated: {
			check: vi.fn(),
			subscribe: readable(false).subscribe
		}
	};
});

vi.mock('$app/environment', () => ({
	browser: true,
	building: false,
	dev: true,
	version: 'test'
}));

// Mock environment variables
vi.mock('$env/dynamic/private', () => ({
	env: {
		FORWARD_EMAIL_API_KEY: 'test-api-key',
		POSTMARK_SERVER_TOKEN: 'test-postmark-token',
		WEBHOOK_SECRET: 'test-webhook-secret',
		TURNSTILE_SECRET_KEY: 'test-turnstile-key'
	}
}));

vi.mock('$env/static/private', () => ({
	FORWARD_EMAIL_API_KEY: 'test-api-key',
	POSTMARK_SERVER_TOKEN: 'test-postmark-token',
	WEBHOOK_SECRET: 'test-webhook-secret',
	TURNSTILE_SECRET_KEY: 'test-turnstile-key'
}));

// Mock Cloudflare platform bindings
export const createMockPlatform = () => ({
	env: {
		DB: createMockD1(),
		R2: createMockR2(),
		KV: createMockKV()
	},
	context: {
		waitUntil: vi.fn()
	},
	caches: {
		default: {
			match: vi.fn(),
			put: vi.fn(),
			delete: vi.fn()
		}
	}
});

// Mock D1 Database
export const createMockD1 = () => ({
	prepare: vi.fn().mockReturnValue({
		bind: vi.fn().mockReturnThis(),
		first: vi.fn().mockResolvedValue(null),
		all: vi.fn().mockResolvedValue({ results: [] }),
		run: vi.fn().mockResolvedValue({ success: true, meta: {} })
	}),
	batch: vi.fn().mockResolvedValue([]),
	exec: vi.fn().mockResolvedValue({ count: 0 }),
	dump: vi.fn().mockResolvedValue(new ArrayBuffer(0))
});

// Mock R2 Bucket
export const createMockR2 = () => ({
	put: vi.fn().mockResolvedValue({ key: 'test-key' }),
	get: vi.fn().mockResolvedValue(null),
	delete: vi.fn().mockResolvedValue(undefined),
	list: vi.fn().mockResolvedValue({ objects: [], truncated: false }),
	head: vi.fn().mockResolvedValue(null)
});

// Mock KV Namespace
export const createMockKV = () => ({
	get: vi.fn().mockResolvedValue(null),
	getWithMetadata: vi.fn().mockResolvedValue({ value: null, metadata: null }),
	put: vi.fn().mockResolvedValue(undefined),
	delete: vi.fn().mockResolvedValue(undefined),
	list: vi.fn().mockResolvedValue({ keys: [], list_complete: true })
});

// Utility for creating test requests
export const createTestRequest = (
	method: string,
	path: string,
	options: {
		body?: unknown;
		headers?: Record<string, string>;
	} = {}
): Request => {
	const url = new URL(path, 'http://localhost');
	const init: RequestInit = {
		method,
		headers: {
			'Content-Type': 'application/json',
			...options.headers
		}
	};

	if (options.body && method !== 'GET') {
		init.body = JSON.stringify(options.body);
	}

	return new Request(url.toString(), init);
};

// Helper to wait for async operations
export const flushPromises = () => new Promise((resolve) => setTimeout(resolve, 0));

// Helper to create test crypto keys (for testing encryption)
export const createTestCryptoKey = async (): Promise<CryptoKey> => {
	return await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, [
		'encrypt',
		'decrypt'
	]);
};

// Global test timeout
vi.setConfig({ testTimeout: 10000 });
