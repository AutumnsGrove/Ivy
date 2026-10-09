/** The smallest a wide mail is shrunk to; below this the text is unreadable and the rest is clipped. */
export const MIN_FIT = 0.4;

/**
 * How far to shrink a mail body so its content fits the frame. The sanitizer
 * drops <style> blocks, so a mail's responsive rules are gone and a fixed-width
 * layout (the usual 600px column) arrives wider than a phone. Shrinking it, as
 * mail apps do, beats clipping it or scrolling sideways inside the page.
 */
export function fitScale(contentWidth: number, availableWidth: number): number {
	if (contentWidth <= 0 || availableWidth <= 0) return 1;
	// Sub-pixel rounding in scrollWidth must not shrink a mail that fits.
	if (contentWidth <= availableWidth + 1) return 1;
	return Math.max(MIN_FIT, availableWidth / contentWidth);
}
