import type { FolderView } from './types.js';

const TITLES: Record<FolderView, string> = {
	inbox: 'Inbox',
	archive: 'Archive',
	trash: 'Trash',
	junk: 'Junk'
};

/** The `?folder=` query value as a folder view; anything unknown is the inbox. */
export function folderOf(url: URL): FolderView {
	const v = url.searchParams.get('folder');
	return v && Object.hasOwn(TITLES, v) ? (v as FolderView) : 'inbox';
}

export const folderTitle = (folder: FolderView): string => TITLES[folder];
