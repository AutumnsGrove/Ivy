<script lang="ts">
	import Pill from '../ui/Pill.svelte';

	type Props = {
		from: string;
		/** CSS colour for the account dot, e.g. `var(--acct-2)`. */
		accountColor: string;
		time: string;
		subject: string;
		preview: string;
		unread?: boolean;
		needs?: boolean;
		tag?: string;
		selected?: boolean;
		/** Phone: navigates to the message. Omit on desktop, where `onselect` fills the reading pane. */
		href?: string;
		onselect?: () => void;
	};
	let {
		from,
		accountColor,
		time,
		subject,
		preview,
		unread = false,
		needs = false,
		tag,
		selected = false,
		href,
		onselect
	}: Props = $props();
</script>

{#snippet body()}
	<span class="top">
		<span class="dot" style:background={accountColor}></span>
		<span class="from ell">{from}</span>
		<span class="time">{time}</span>
		{#if unread}
			<span class="unread" aria-hidden="true"></span>
			<span class="sr-only">Unread</span>
		{/if}
	</span>
	<span class="subject ell" data-unread={unread}>{subject}</span>
	<span class="preview ell">{preview}</span>
	{#if needs || tag}
		<span class="tags">
			{#if needs}<Pill tone="need">needs you</Pill>{/if}
			{#if tag}<Pill>{tag}</Pill>{/if}
		</span>
	{/if}
{/snippet}

{#if href}
	<a {href} class="card" class:selected aria-current={selected ? 'true' : undefined}>
		{@render body()}
	</a>
{:else}
	<button
		type="button"
		class="card"
		class:selected
		aria-current={selected ? 'true' : undefined}
		onclick={onselect}
	>
		{@render body()}
	</button>
{/if}

<style>
	.card {
		display: flex;
		flex-direction: column;
		gap: var(--sp-3);
		width: 100%;
		padding: var(--sp-12) var(--sp-14);
		border-radius: var(--radius-card);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
		text-align: left;
		color: var(--text);
	}
	.selected {
		background: var(--accent-soft);
		border-color: var(--accent-line);
	}
	.top {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.dot {
		width: var(--sp-8);
		height: var(--sp-8);
		border-radius: 50%;
		flex: none;
	}
	.from {
		flex-grow: 1;
		min-width: 0;
		font-size: var(--fs-ui-lg);
		font-weight: 500;
	}
	.time {
		flex: none;
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.unread {
		width: var(--sp-8);
		height: var(--sp-8);
		border-radius: 50%;
		flex: none;
		background: var(--accent);
		box-shadow: var(--glow-accent);
	}
	.subject {
		font-size: var(--fs-ui);
		font-weight: 400;
	}
	.subject[data-unread='true'] {
		font-weight: 500;
	}
	.preview {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.tags {
		display: flex;
		gap: var(--sp-6);
		margin-top: var(--sp-5);
	}
</style>
