// What is selected in a message list while the operator is choosing several to
// act on together (issue #11). It holds ids only; the list owns the messages.

/** The most one batch can carry (gateway maxBatchMessages). */
export const MAX_SELECTION = 200;

export class Selection {
	on = $state(false);
	ids = $state<string[]>([]);

	get count() {
		return this.ids.length;
	}

	has(id: string) {
		return this.ids.includes(id);
	}

	start() {
		this.on = true;
		this.ids = [];
	}

	exit() {
		this.on = false;
		this.ids = [];
	}

	/** False when nothing changed: selection is off, or the batch is full. */
	toggle(id: string): boolean {
		if (!this.on) return false;
		if (this.has(id)) {
			this.ids = this.ids.filter((x) => x !== id);
			return true;
		}
		if (this.ids.length >= MAX_SELECTION) return false;
		this.ids = [...this.ids, id];
		return true;
	}

	/** Chooses what is loaded, in list order; true when the batch limit cut it short. */
	selectAll(loaded: string[]): boolean {
		this.ids = loaded.slice(0, MAX_SELECTION);
		return loaded.length > MAX_SELECTION;
	}

	/** Whether every loaded message is chosen. */
	allOf(loaded: string[]) {
		return loaded.length > 0 && loaded.every((id) => this.has(id));
	}

	toggleAll(loaded: string[]): boolean {
		if (this.allOf(loaded)) {
			this.ids = [];
			return false;
		}
		return this.selectAll(loaded);
	}

	/** Drops ids that have left the list (acted on, or hidden by a live op). */
	prune(loaded: string[]) {
		const keep = new Set(loaded);
		if (this.ids.every((id) => keep.has(id))) return;
		this.ids = this.ids.filter((id) => keep.has(id));
	}
}
