// Transient UI state shared across components: nothing here is persisted or sent anywhere.
class Ui {
	/** The phone's account / folder / tag side panel. */
	drawerOpen = $state(false);
}

export const ui = new Ui();
