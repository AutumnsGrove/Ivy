// Mutations are not wired to a server yet; these give the right feedback so the screens feel finished.
// Per the plan, moving or deleting always goes to IMAP first and always offers undo.
import { goto } from '$app/navigation';
import { toasts } from './toast';

export function archiveMessage(back: string) {
	toasts.push({ text: 'Archived', action: { label: 'Undo', run: () => void goto(back) } });
}

export function deleteMessage(back: string) {
	toasts.push({ text: 'Moved to Trash', action: { label: 'Undo', run: () => void goto(back) } });
}
