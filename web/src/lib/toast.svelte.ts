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

export type Toast = Required<Pick<ToastInput, 'text' | 'tone' | 'duration'>> &
	Pick<ToastInput, 'detail' | 'action'> & { id: number };

const DEFAULT_MS = 4000;

class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;
	#timers = new Map<number, ReturnType<typeof setTimeout>>();

	push(input: ToastInput): number {
		const toast: Toast = {
			id: this.#next++,
			text: input.text,
			detail: input.detail,
			action: input.action,
			tone: input.tone ?? 'plain',
			duration: input.duration ?? DEFAULT_MS
		};
		this.items.push(toast);
		if (toast.duration > 0) {
			this.#timers.set(
				toast.id,
				setTimeout(() => this.dismiss(toast.id), toast.duration)
			);
		}
		return toast.id;
	}

	dismiss(id: number) {
		clearTimeout(this.#timers.get(id));
		this.#timers.delete(id);
		this.items = this.items.filter((t) => t.id !== id);
	}

	/** Runs the toast's action exactly once, then removes it. */
	act(id: number) {
		const toast = this.items.find((t) => t.id === id);
		if (!toast) return;
		this.dismiss(id);
		toast.action?.run();
	}

	clear() {
		for (const id of this.#timers.keys()) clearTimeout(this.#timers.get(id));
		this.#timers.clear();
		this.items = [];
	}
}

export const toasts = new Toasts();
