<script lang="ts">
	import { Inbox, BookOpen, Search, Tag, SlidersHorizontal } from '#lib/icons.js';

	let { current }: { current: string } = $props();

	const tabs = [
		{ href: '/', label: 'Inbox', icon: Inbox },
		{ href: '/reading', label: 'Reading', icon: BookOpen },
		{ href: '/search', label: 'Search', icon: Search },
		{ href: '/tags', label: 'Tags', icon: Tag },
		{ href: '/settings', label: 'Settings', icon: SlidersHorizontal }
	];

	// Inbox owns "/" and the message routes under it; other tabs own their sub-routes.
	const active = (href: string) =>
		href === '/'
			? current === '/' || current.startsWith('/m/') || current.startsWith('/compose')
			: current === href || current.startsWith(href + '/');
</script>

<nav class="bar" aria-label="Main">
	{#each tabs as { href, label, icon: Icon } (href)}
		<a {href} class="tab" class:on={active(href)} aria-current={active(href) ? 'page' : undefined}>
			<Icon />
			{label}
		</a>
	{/each}
</nav>

<style>
	.bar {
		display: flex;
		align-items: center;
		height: var(--sp-58);
		padding: 0 var(--sp-6);
		border-radius: var(--radius-bar);
		background: var(--glass-strong);
		border: 1px solid var(--glass-border);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
	}
	.tab {
		display: flex;
		flex: 1;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 2px;
		height: var(--sp-50);
		border-radius: var(--radius-card);
		color: var(--faint);
		font: 400 var(--fs-tab) var(--font-ui);
	}
	.tab.on {
		color: var(--accent);
	}
</style>
