<script lang="ts">
	import { CircleAlert } from '#lib/icons.js';
	import type { MailSummary } from '#lib/types.js';
	import Avatar from '../ui/Avatar.svelte';
	import Button from '../ui/Button.svelte';

	type Props = { summary: MailSummary; color: string; onretry: () => void };
	let { summary, color, onretry }: Props = $props();
	let showPreview = $state(false);
</script>

<article>
	<h1 class="subject">{summary.subject}</h1>
	<div class="from">
		<Avatar initials={summary.initials} {color} size="lg" />
		<div>
			<div class="name">{summary.from}</div>
			<div class="when">{summary.time}</div>
		</div>
	</div>

	<div class="fail" role="alert">
		<span class="icon"><CircleAlert /></span>
		<h2>This message didn't load</h2>
		<p>Ivy couldn't fetch the full text just now. The message is still there.</p>
		<div class="actions">
			<Button variant="primary" onclick={onretry}>Try again</Button>
			<Button onclick={() => (showPreview = true)}>Show what we have</Button>
		</div>
	</div>

	{#if showPreview}<p class="preview">{summary.preview}</p>{/if}
</article>

<style>
	.subject {
		font: 500 var(--fs-read-title) / 1.22 var(--font-read);
	}
	.from {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		margin-top: var(--sp-16);
	}
	.name {
		font-size: var(--fs-ui-lg);
		font-weight: 500;
	}
	.when {
		margin-top: 2px;
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.fail {
		margin-top: var(--sp-24);
		padding: var(--sp-20) var(--sp-18);
		border-radius: var(--radius-group);
		border: 1px solid var(--danger-line);
		background: var(--danger-soft);
		text-align: center;
	}
	.icon {
		display: inline-flex;
		color: var(--danger);
	}
	.icon :global(svg) {
		width: var(--sp-28);
		height: var(--sp-28);
	}
	h2 {
		margin-top: var(--sp-10);
		font: 500 var(--fs-h2) var(--font-read);
	}
	.fail p {
		margin: var(--sp-6) 0 var(--sp-16);
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sp-10);
		justify-content: center;
	}
	.preview {
		margin-top: var(--sp-20);
		font: 400 var(--fs-read-sm) / 1.6 var(--font-read);
		color: var(--muted);
	}
</style>
