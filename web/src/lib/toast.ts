// Our one way to show a toast. svelte-sonner draws and times them (stacking, swipe to dismiss,
// pause on hover, screen-reader announcements); this facade keeps call sites independent of it and
// carries the plan's rules: undo where it makes sense, and things that need you never auto-close.
import { toast } from 'svelte-sonner';

export type ToastTone = 'plain' | 'ok' | 'warn' | 'danger' | 'info';

export type ToastInput = {
	text: string;
	/** Second line: what is safe, or what Ivy will do next. */
	detail?: string;
	tone?: ToastTone;
	action?: { label: string; run: () => void };
	/** Milliseconds; 0 keeps it until dismissed (things that need you). */
	duration?: number;
};

const DEFAULT_MS = 4000;

const show = {
	plain: toast,
	ok: toast.success,
	warn: toast.warning,
	danger: toast.error,
	info: toast.info
} as const;

export const toasts = {
	push(input: ToastInput): string | number {
		const { text, detail, tone = 'plain', action, duration = DEFAULT_MS } = input;
		return show[tone](text, {
			description: detail,
			duration: duration === 0 ? Infinity : duration,
			action: action && { label: action.label, onClick: () => action.run() }
		});
	},

	dismiss(id: string | number) {
		toast.dismiss(id);
	},

	clear() {
		toast.dismiss();
	}
};
