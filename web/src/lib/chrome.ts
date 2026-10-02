// The five top-level screens carry the tab bar; everything pushed on top of them
// (a message, compose, settings, a tag) takes the full screen and brings its own back button.
const TAB_SCREENS = new Set(['/', '/reading', '/search', '/ask', '/tags']);

export function showsTabBar(pathname: string): boolean {
	return TAB_SCREENS.has(pathname.replace(/\/+$/, '') || '/');
}
