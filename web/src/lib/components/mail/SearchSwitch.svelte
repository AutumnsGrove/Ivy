<script lang="ts">
	import { MessageCircle, Search } from '#lib/icons.js';
	import Segmented from '../ui/Segmented.svelte';

	/** The Search / Ask Ivy switch that heads both screens. */
	let { current }: { current: 'search' | 'ask' } = $props();
</script>

<Segmented
	label="Search or ask"
	size="lg"
	glass
	value={current}
	options={[
		{ value: 'search', label: 'Search', href: '/search', icon: Search },
		{ value: 'ask', label: 'Ask Ivy', href: '/ask', icon: MessageCircle }
	]}
/>
