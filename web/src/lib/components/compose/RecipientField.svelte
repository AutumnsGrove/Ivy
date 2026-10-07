<script lang="ts">
	import { X } from '#lib/icons.js';
	import type { Person } from '#lib/types.js';
	import { parseRecipients, suggestPeople } from '#lib/compose/recipients.js';

	type Props = {
		label: string;
		recipients: string[];
		people: Person[];
		placeholder?: string;
		/** Called whenever a chip is added or removed, so the screen marks the draft dirty. */
		onchange?: () => void;
	};
	let { label, recipients = $bindable(), people, placeholder, onchange }: Props = $props();

	let query = $state('');
	let focused = $state(false);
	const suggestions = $derived(focused ? suggestPeople(people, query, recipients) : []);

	function commit(raw: string) {
		const parsed = parseRecipients(raw);
		if (parsed.accepted.length) {
			recipients = [...recipients, ...parsed.accepted.filter((a) => !recipients.some((r) => r.toLowerCase() === a.toLowerCase()))];
			onchange?.();
		}
		query = parsed.rejected.join(', ');
	}

	function pick(person: Person) {
		commit(person.email);
	}

	function remove(address: string) {
		recipients = recipients.filter((r) => r !== address);
		onchange?.();
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'Enter' || event.key === ',' || event.key === ';') {
			event.preventDefault();
			commit(query);
			return;
		}
		if (event.key === 'Backspace' && !query && recipients.length) {
			recipients = recipients.slice(0, -1);
			onchange?.();
		}
	}
</script>

<div class="fl">
	<span class="k" id="{label}-label">{label}</span>
	<div class="line">
		{#each recipients as address (address)}
			<span class="chip">
				{address}
				<button type="button" aria-label="Remove {address}" onclick={() => remove(address)}><X /></button>
			</span>
		{/each}
		<input
			aria-labelledby="{label}-label"
			bind:value={query}
			{placeholder}
			autocomplete="off"
			onfocus={() => (focused = true)}
			onblur={() => {
				focused = false;
				commit(query);
			}}
			onkeydown={onkeydown}
			oninput={(e) => (e.currentTarget.value.includes(',') ? commit(e.currentTarget.value) : null)}
		/>
	</div>
	{#if suggestions.length}
		<ul class="suggest" role="listbox" aria-label="{label} suggestions">
			{#each suggestions as person (person.id)}
				<li>
					<button
						type="button"
						role="option"
						aria-selected="false"
						onmousedown={(e) => e.preventDefault()}
						onclick={() => pick(person)}
					>
						<span class="nm">{person.name}</span>
						<span class="em">{person.email}</span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.fl {
		position: relative;
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-48);
		padding: 0 var(--sp-4);
		border-bottom: 1px solid var(--glass-border);
		font-size: var(--fs-ui-lg);
	}
	.k {
		width: var(--sp-52);
		flex: none;
		font-size: var(--fs-aside);
		color: var(--faint);
	}
	.line {
		display: flex;
		align-items: center;
		flex: 1;
		flex-wrap: wrap;
		gap: var(--sp-6);
		min-width: 0;
		padding: var(--sp-4) 0;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-4);
		height: var(--sp-28);
		padding: 0 var(--sp-4) 0 var(--sp-10);
		border-radius: var(--radius-chip);
		border: 1px solid var(--accent-line);
		background: var(--accent-soft);
		font-size: var(--fs-small);
		white-space: nowrap;
	}
	.chip button {
		display: grid;
		place-items: center;
		width: var(--sp-18);
		height: var(--sp-18);
		padding: 0;
		border: 0;
		border-radius: 50%;
		background: transparent;
		color: var(--muted);
	}
	.chip :global(svg) {
		width: var(--sp-12);
		height: var(--sp-12);
	}
	input {
		flex: 1;
		min-width: var(--sp-120);
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		outline: none;
	}
	.suggest {
		position: absolute;
		left: var(--sp-58);
		top: 100%;
		z-index: var(--z-bar);
		display: flex;
		flex-direction: column;
		min-width: var(--sp-240);
		margin: 0;
		padding: var(--sp-4);
		list-style: none;
		border: 1px solid var(--glass-border);
		border-radius: var(--radius-card);
		background: var(--glass-strong);
		backdrop-filter: var(--blur-strong);
		box-shadow: var(--shadow-card);
	}
	.suggest button {
		display: flex;
		flex-direction: column;
		width: 100%;
		padding: var(--sp-8) var(--sp-10);
		border: 0;
		border-radius: var(--radius-md);
		background: transparent;
		color: var(--text);
		text-align: left;
	}
	.nm {
		font-size: var(--fs-aside);
	}
	.em {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
</style>
