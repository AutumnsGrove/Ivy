// Phone layout (tab bar) below this width, desktop layout (rail + panes) at or above it.
// iPad portrait lands on the phone layout in a centred column; landscape gets the panes.
// Media queries cannot read CSS custom properties, so the breakpoint lives here and in the layout CSS.
const DESKTOP = '(min-width: 900px)';

class Viewport {
	isDesktop = $state(false);

	/** Begins tracking the window; returns the function that stops it. */
	start(): () => void {
		const mq = matchMedia(DESKTOP);
		this.isDesktop = mq.matches;
		const onChange = (e: { matches: boolean }) => (this.isDesktop = e.matches);
		mq.addEventListener('change', onChange);
		return () => mq.removeEventListener('change', onChange);
	}
}

export const viewport = new Viewport();
