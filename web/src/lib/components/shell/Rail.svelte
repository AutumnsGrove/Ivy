<script lang="ts">
	import { BookOpen, Inbox, Search, SlidersHorizontal, Tag } from '#lib/icons.js';

	let { current }: { current: string } = $props();

	const main = [
		{ href: '/', label: 'Inbox', icon: Inbox },
		{ href: '/reading', label: 'Reading feed', icon: BookOpen },
		{ href: '/search', label: 'Search', icon: Search },
		{ href: '/tags', label: 'Tags', icon: Tag }
	];
	const active = (href: string) =>
		href === '/'
			? current === '/' || current.startsWith('/m/') || current.startsWith('/compose')
			: current === href || current.startsWith(href + '/') || (href === '/search' && current === '/ask');
</script>

<nav class="rail" aria-label="Main">
	<img class="logo" src="/icon-256.png" alt="Ivy" />
	{#each main as { href, label, icon: Icon } (href)}
		<a {href} class="item" class:on={active(href)} aria-label={label} aria-current={active(href) ? 'page' : undefined}>
			<Icon />
		</a>
	{/each}
	<span class="grow"></span>
	<a
		href="/settings"
		class="item"
		class:on={active('/settings')}
		aria-label="Settings"
		aria-current={active('/settings') ? 'page' : undefined}
	>
		<SlidersHorizontal />
	</a>
</nav>

<style>
	.rail {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--sp-8);
		height: 100%;
		padding: var(--sp-14) 0;
	}
	.logo {
		width: var(--sp-40);
		height: var(--sp-40);
		margin-bottom: var(--sp-10);
	}
	.item {
		display: flex;
		align-items: center;
		justify-content: center;
		width: var(--hit);
		height: var(--hit);
		border-radius: var(--radius-md);
		color: var(--faint);
	}
	.on {
		color: var(--accent);
		background: var(--accent-soft);
	}
	.grow {
		flex-grow: 1;
	}
</style>
