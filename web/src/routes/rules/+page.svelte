<script lang="ts">
	import { Plus } from '#lib/icons.js';
	import RuleSentence from '#lib/components/rules/RuleSentence.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();
	let on = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const r of data.rules) on[r.id] ??= r.on;
	});
</script>

<TopBar title="Rules" backHref="/tags">
	{#snippet trailing()}<IconButton label="New rule" tone="accent" href="/rules/new"><Plus /></IconButton>{/snippet}
</TopBar>

<Page>
	<p class="lede">Small helpers that tidy mail as it arrives.</p>
	<ul class="list">
		{#each data.rules as r (r.id)}
			<li>
				<Glass radius="group" class="rule {on[r.id] ? '' : 'paused'}">
					<div class="top">
						<a href="/rules/{r.id}" class="sentence" aria-label="Edit rule: when {r.when} then {r.then} {r.thenToken}">
							<RuleSentence rule={r} />
						</a>
						<Toggle label="Rule on: {r.when}" bind:checked={on[r.id]} />
					</div>
					<div class="meta">{on[r.id] ? `Matched ${r.matches} times` : 'Paused'}</div>
				</Glass>
			</li>
		{/each}
	</ul>
	<p class="foot">Rules can tag, sort into Reading, or snooze.<br />Moving, deleting and sending always ask you first.</p>
</Page>

<style>
	.lede {
		margin: var(--sp-16) var(--sp-4) var(--sp-14);
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.list :global(.rule) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		padding: var(--sp-14) var(--sp-16);
	}
	.list :global(.rule.paused) {
		opacity: 0.7;
	}
	.top {
		display: flex;
		align-items: flex-start;
		gap: var(--sp-12);
	}
	.sentence {
		flex-grow: 1;
	}
	.meta {
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.foot {
		margin: var(--sp-28) 0 0;
		text-align: center;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
</style>
