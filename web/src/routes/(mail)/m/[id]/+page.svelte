<script lang="ts">
	import MessageScreen from '#lib/components/mail/MessageScreen.svelte';

	let { data } = $props();
</script>

<MessageScreen id={data.id} accounts={data.accounts} scenario={data.scenario} />
