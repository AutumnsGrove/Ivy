/**
 * Svelte Stores
 *
 * Reactive state management for the Ivy client.
 */

import { writable, derived } from 'svelte/store';

// TODO: Define email types
export interface Email {
	id: string;
	threadId: string;
	from: string;
	to: string[];
	subject: string;
	snippet: string;
	date: Date;
	isRead: boolean;
	labels: string[];
}

export interface Thread {
	id: string;
	emails: Email[];
	subject: string;
	participants: string[];
	lastDate: Date;
	isRead: boolean;
	labels: string[];
}

// Email stores
export const emails = writable<Email[]>([]);
export const threads = writable<Thread[]>([]);
export const selectedThreadId = writable<string | null>(null);

// Derived stores
export const selectedThread = derived(
	[threads, selectedThreadId],
	([$threads, $selectedThreadId]) => $threads.find((t) => t.id === $selectedThreadId) ?? null
);

// UI state
export const isComposing = writable(false);
export const searchQuery = writable('');

// User state
export const currentUser = writable<{ id: string; email: string } | null>(null);
