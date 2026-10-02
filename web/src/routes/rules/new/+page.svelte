<script lang="ts">
	import { goto } from '$app/navigation';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let text = $state('');
	const ideas = [
		'Send newsletters to Reading',
		'Tag anything about a job application as jobs',
		'Snooze invoices until Monday morning'
	];
</script>

<TopBar title="New rule" backHref="/rules" back="close" />

<Page>
	<h1>What would you like to happen?</h1>
	<p class="lede">Say it your way. Ivy turns it into a rule for you to check.</p>

	<Glass variant="strong" radius="note" class="box">
		<textarea bind:value={text} aria-label="Describe your rule" placeholder="I want emails from Cloudflare that look like receipts tagged as such"></textarea>
	</Glass>

	<GroupLabel>Ideas</GroupLabel>
	<ul class="ideas">
		{#each ideas as i}
			<li><button type="button" onclick={() => (text = i)}>{i}</button></li>
		{/each}
	</ul>

	<div class="cta">
		<Button variant="primary" size="xl" block disabled={!text.trim()} onclick={() => goto('/rules/new/review')}>Make this rule</Button>
		<p><Glint />Written once. After that, it just runs.</p>
	</div>
</Page>

<style>
	h1 {
		margin-top: var(--sp-24);
		font: 500 var(--fs-brand) / 1.15 var(--font-read);
		font-size: var(--fs-read-title-lg);
	}
	.lede {
		margin-top: var(--sp-8);
		font-size: var(--fs-ui);
		line-height: 1.5;
		color: var(--muted);
	}
	:global(.box) {
		margin-top: var(--sp-22);
		min-height: var(--sp-132);
		padding: var(--sp-16) var(--sp-18);
		border-color: var(--accent-line);
	}
	textarea {
		width: 100%;
		min-height: var(--sp-110);
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-read) / 1.5 var(--font-read);
		resize: none;
		outline: none;
	}
	.ideas {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.ideas button {
		width: 100%;
		padding: var(--sp-12) var(--sp-14);
		border-radius: var(--radius-chip);
		border: 1px solid var(--glass-border);
		background: var(--wash-soft);
		color: var(--muted);
		text-align: left;
		font: 400 var(--fs-aside) / 1.4 var(--font-ui);
	}
	.cta {
		margin-top: var(--sp-32);
		text-align: center;
	}
	.cta p {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: var(--sp-8);
		margin-top: var(--sp-12);
		font-size: var(--fs-note);
		color: var(--faint);
	}
</style>
