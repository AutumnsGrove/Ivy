<script lang="ts">
	import PhoneInbox from '#lib/components/mail/PhoneInbox.svelte';

	let { data } = $props();
</script>

<PhoneInbox {...data} />
