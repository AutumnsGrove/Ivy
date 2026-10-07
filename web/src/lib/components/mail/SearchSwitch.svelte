<script lang="ts">
	import { MessageCircle, Search, Users } from '#lib/icons.js';
	import Segmented from '../ui/Segmented.svelte';

	/** The Search / Ask Ivy / People switch that heads all three screens. */
	let { current }: { current: 'search' | 'ask' | 'people' } = $props();
</script>

<Segmented
	label="Search, ask or people"
	size="lg"
	glass
	value={current}
	options={[
		{ value: 'search', label: 'Search', href: '/search', icon: Search },
		{ value: 'ask', label: 'Ask Ivy', href: '/ask', icon: MessageCircle },
		{ value: 'people', label: 'People', href: '/people', icon: Users }
	]}
/>
