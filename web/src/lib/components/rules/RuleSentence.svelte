<script lang="ts">
	import type { Rule } from '#lib/types.js';
	import Dot from '../ui/Dot.svelte';

	let { rule }: { rule: Pick<Rule, 'when' | 'whenToken' | 'whenTail' | 'then' | 'thenToken' | 'thenColor'> } = $props();
</script>

<p class="sent">
	<span class="k">When</span>{rule.when}
	{#if rule.whenToken}<span class="tok">{rule.whenToken}</span>{/if}
	{#if rule.whenTail}{rule.whenTail}{/if}
	<br />
	<span class="k">Then</span>{rule.then}
	<span class="tok">
		{#if rule.thenColor}<Dot color="var(--tag-{rule.thenColor})" />{/if}{rule.thenToken}
	</span>
</p>

<style>
	.sent {
		font: 400 var(--fs-ui-lg) / 1.7 var(--font-ui);
		color: var(--muted);
	}
	.k {
		margin-right: var(--sp-4);
		font-size: var(--fs-note);
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--faint);
	}
	.tok {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-6);
		height: var(--sp-26);
		margin: 0 2px;
		padding: 0 var(--sp-10);
		border-radius: var(--sp-13);
		background: var(--accent-soft);
		border: 1px solid var(--accent-line);
		color: var(--text);
		font-size: var(--fs-aside);
	}
</style>
