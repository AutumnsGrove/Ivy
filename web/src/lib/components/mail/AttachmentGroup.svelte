<script lang="ts">
	import { Download, FileText, CircleAlert } from '#lib/icons.js';
	import type { Attachment } from '#lib/types.js';

	type Props = {
		attachments: Attachment[];
		/** Desktop reading pane: bigger tiles, files inline with the thumbnails. */
		wide?: boolean;
		onretry?: (a: Attachment) => void;
	};
	let { attachments, wide = false, onretry }: Props = $props();

	const images = $derived(attachments.filter((a) => a.kind === 'image' && !a.failed));
	const files = $derived(attachments.filter((a) => a.kind === 'file' || a.failed));
	const shownImages = $derived(wide ? images : images.slice(0, 2));
	const hidden = $derived(wide ? 0 : images.length - shownImages.length);
</script>

{#if attachments.length}
	<div class="group" class:wide>
		{#if shownImages.length}
			<ul class="thumbs">
				{#each shownImages as a (a.id)}
					<li class="thumb tone-{a.tone ?? 'a'}">
						<span class="name">{a.name}</span>
					</li>
				{/each}
				{#if hidden > 0}<li class="more">+{hidden} more</li>{/if}
				{#if wide}
					{#each files as a (a.id)}
						<li class="file inline">
							<span class="ficon"><FileText /></span>
							<span class="meta"><span>{a.name}</span><span class="size">{a.size}</span></span>
						</li>
					{/each}
				{/if}
			</ul>
		{/if}
		{#if !wide}
			<ul class="files">
				{#each files as a (a.id)}
					<li class="file" class:bad={a.failed}>
						<span class="ficon">{#if a.failed}<CircleAlert />{:else}<FileText />{/if}</span>
						<span class="meta">
							<span>{a.name}</span>
							<span class="size" class:err={a.failed}>{a.failed ? "Couldn't load" : a.size}</span>
						</span>
						{#if a.failed}
							<button type="button" class="retry" onclick={() => onretry?.(a)}>Retry</button>
						{:else}
							<span class="dl"><Download /></span>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</div>
{/if}

<style>
	.group {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
	}
	ul {
		display: flex;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.thumbs {
		gap: var(--sp-8);
	}
	.wide .thumbs {
		gap: var(--sp-10);
	}
	.files {
		flex-direction: column;
		gap: var(--sp-8);
	}
	.thumb,
	.more {
		position: relative;
		width: calc(var(--sp-110) - var(--sp-6));
		height: calc(var(--sp-84) - var(--sp-6));
		border-radius: var(--radius-md);
		border: 1px solid var(--glass-border);
		overflow: hidden;
	}
	.wide .thumb {
		width: var(--sp-132);
		height: calc(var(--sp-90) + 2px);
	}
	.tone-a {
		background: var(--attach-a);
	}
	.tone-b {
		background: var(--attach-b);
	}
	.name {
		position: absolute;
		left: var(--sp-8);
		bottom: var(--sp-6);
		font-size: var(--fs-label);
		color: var(--attach-label);
	}
	.more {
		display: flex;
		flex-grow: 1;
		align-items: center;
		justify-content: center;
		border-style: dashed;
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.file {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding: var(--sp-9) var(--sp-12);
		border-radius: var(--radius-md);
		border: 1px solid var(--glass-border);
	}
	.file.inline {
		height: calc(var(--sp-90) + 2px);
		padding: 0 var(--sp-14);
	}
	.bad {
		border-color: var(--danger-line);
	}
	.ficon {
		display: flex;
		color: var(--accent);
	}
	.bad .ficon {
		color: var(--danger);
	}
	.meta {
		display: flex;
		flex-direction: column;
		flex-grow: 1;
		min-width: 0;
		font-size: var(--fs-small);
	}
	.size {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.err {
		color: var(--danger);
	}
	.dl {
		display: flex;
		color: var(--faint);
	}
	.dl :global(svg) {
		width: var(--sp-16);
		height: var(--sp-16);
	}
	.retry {
		border: 0;
		background: transparent;
		color: var(--accent);
		font: 400 var(--fs-aside) var(--font-ui);
	}
</style>
