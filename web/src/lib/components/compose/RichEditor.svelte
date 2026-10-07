<script module lang="ts">
	export type RichEditorApi = {
		bold: () => void;
		italic: () => void;
		toggleList: () => void;
		makeLink: (url: string) => void;
		removeLink: () => void;
		insertImage: (cid: string, alt: string) => void;
		setHtml: (html: string) => void;
		focus: () => void;
	};

	export type RichFormatState = { bold: boolean; italic: boolean; list: boolean; link: boolean };
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import Squire from 'squire-rte';
	import { sanitizeToFragment } from '#lib/compose/sanitize.js';

	let {
		value = $bindable(''),
		label = 'Message body',
		onpath,
		oninput,
		onapi
	}: {
		value?: string;
		label?: string;
		onpath?: (state: RichFormatState) => void;
		oninput?: () => void;
		onapi?: (api: RichEditorApi | undefined) => void;
	} = $props();

	let root: HTMLDivElement;
	let editor: Squire | undefined;

	const formatState = (): RichFormatState => ({
		bold: Boolean(editor?.hasFormat('B') || editor?.hasFormat('STRONG')),
		italic: Boolean(editor?.hasFormat('I') || editor?.hasFormat('EM')),
		list: Boolean(editor?.hasFormat('UL') || editor?.hasFormat('OL')),
		link: Boolean(editor?.hasFormat('A'))
	});

	// Reflect the editor's HTML back to the page and refresh the button state.
	function publish() {
		if (!editor) return;
		value = editor.getHTML();
		onpath?.(formatState());
	}

	onMount(() => {
		editor = new Squire(root, {
			blockTag: 'P',
			// Squire refuses to load HTML without a sanitiser; ours is the same
			// allow-list the server narrows outgoing mail to, minus the reader policy.
			sanitizeToDOMFragment: (html, ed) => sanitizeToFragment(html, ed.getRoot().ownerDocument)
		});
		editor.setHTML(value ?? '');
		editor.addEventListener('input', () => {
			publish();
			oninput?.();
		});
		editor.addEventListener('pathChange', () => onpath?.(formatState()));
		editor.addEventListener('focus', () => onpath?.(formatState()));
		// Squire makes the node contenteditable; give it a name for assistive tech
		// and for tests, since it is not a native form control.
		root.setAttribute('role', 'textbox');
		root.setAttribute('aria-multiline', 'true');
		root.setAttribute('aria-label', label);

		onapi?.({
			bold: () => {
				editor?.bold();
				publish();
			},
			italic: () => {
				editor?.italic();
				publish();
			},
			toggleList: () => {
				if (!editor) return;
				if (editor.hasFormat('UL') || editor.hasFormat('OL')) editor.removeList();
				else editor.makeUnorderedList();
				publish();
			},
			makeLink: (url) => {
				editor?.makeLink(url);
				publish();
			},
			removeLink: () => {
				editor?.removeLink();
				publish();
			},
			insertImage: (cid, alt) => {
				editor?.insertImage(`cid:${cid}`, alt ? { alt } : {});
				publish();
			},
			setHtml: (html) => {
				editor?.setHTML(html);
				value = html;
			},
			focus: () => editor?.focus()
		});
		return () => {
			onapi?.(undefined);
			editor?.destroy();
		};
	});
</script>

<div class="rich" bind:this={root}></div>

<style>
	.rich {
		width: 100%;
		min-height: var(--sp-160);
		margin-top: var(--sp-16);
		color: var(--text);
		font: 400 var(--fs-title-sm) / 1.65 var(--font-read);
		outline: none;
	}
	.rich :global(p) {
		margin: 0 0 var(--sp-12);
	}
	.rich :global(ul),
	.rich :global(ol) {
		margin: 0 0 var(--sp-12);
		padding-left: var(--sp-24);
	}
	.rich :global(a) {
		color: var(--accent);
	}
	.rich :global(blockquote) {
		margin: 0 0 var(--sp-12);
		padding-left: var(--sp-12);
		border-left: 1px solid var(--glass-border);
		color: var(--muted);
	}
	.rich :global(img) {
		max-width: 100%;
	}
</style>
