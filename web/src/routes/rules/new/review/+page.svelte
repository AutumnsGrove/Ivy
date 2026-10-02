<script lang="ts">
	import { goto } from '$app/navigation';
	import { Check, X } from '#lib/icons.js';
	import RuleSentence from '#lib/components/rules/RuleSentence.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.svelte.js';

	const rule = {
		when: 'mail is from',
		whenToken: 'Cloudflare',
		whenTail: 'and looks like a receipt',
		then: 'tag it',
		thenToken: 'receipts',
		thenColor: 'sky' as const
	};
	const results = [
		{ s: 'Receipt for your renewal', tagged: true },
		{ s: 'Invoice for your plan', tagged: true },
		{ s: 'Dev Day: save the date', tagged: false },
		{ s: "What's new this quarter", tagged: false }
	];

	function turnOn() {
		toasts.push({ text: 'Rule is on', tone: 'ok' });
		void goto('/rules');
	}
</script>

<TopBar title="Check your rule" backHref="/rules/new" />

<Page>
	<GroupLabel>Here is what I understood</GroupLabel>
	<Glass radius="note" class="card">
		<RuleSentence {rule} />
		<p class="why">Only real receipts get tagged. Cloudflare's other mail, like announcements, is left alone.</p>
	</Glass>

	<GroupLabel>On your last 200 messages</GroupLabel>
	<Glass radius="group" class="res">
		{#each results as r}
			<div class="r" class:left={!r.tagged}>
				<span class="i">{#if r.tagged}<Check />{:else}<X />{/if}</span>
				<span class="m"><span class="ell s">{r.s}</span><span class="d">Cloudflare · {r.tagged ? 'tagged' : 'left alone'}</span></span>
			</div>
		{/each}
	</Glass>
	<p class="sum">5 would be tagged, 6 more from Cloudflare would be left alone. Does that look right?</p>

	<div class="cta">
		<Button size="xl" block href="/rules/r1">Edit details</Button>
		<Button size="xl" variant="primary" block onclick={turnOn}>Turn it on</Button>
	</div>
	<p class="safe">Nothing happens until you turn it on.</p>
</Page>

<style>
	:global(.card) {
		padding: var(--sp-16) var(--sp-18);
		border-color: var(--accent-line);
	}
	.why {
		margin-top: var(--sp-10);
		padding-top: var(--sp-12);
		border-top: 1px solid var(--glass-border);
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	:global(.res) {
		padding: var(--sp-6) var(--sp-16);
	}
	.r {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-48);
		border-bottom: 1px solid var(--glass-border);
	}
	.r:last-child {
		border-bottom: 0;
	}
	.i {
		display: flex;
		color: var(--ok);
	}
	.left .i,
	.left .s {
		color: var(--muted);
	}
	.m {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.s {
		font-size: var(--fs-ui);
	}
	.d {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.sum {
		margin: var(--sp-10) var(--sp-8) 0;
		font-size: var(--fs-small);
		line-height: 1.5;
		color: var(--muted);
	}
	.cta {
		display: flex;
		gap: var(--sp-10);
		margin-top: var(--sp-28);
	}
	.safe {
		margin-top: var(--sp-12);
		text-align: center;
		font-size: var(--fs-meta);
		color: var(--faint);
	}
</style>
