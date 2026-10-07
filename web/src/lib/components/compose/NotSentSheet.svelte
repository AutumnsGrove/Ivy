<script lang="ts">
	import { CircleAlert } from '#lib/icons.js';
	import Button from '../ui/Button.svelte';
	import Sheet from '../ui/Sheet.svelte';

	type Props = {
		open: boolean;
		to: string;
		/** The server's plain wording for why it was not sent. */
		reason: string;
		onback?: () => void;
	};
	let { open = $bindable(), to, reason, onback }: Props = $props();
</script>

<Sheet bind:open title="Not sent">
	<div class="head">
		<span class="orb"><CircleAlert /></span>
		<div>
			<h2>Not sent</h2>
			<p class="to">to {to || 'your recipient'}</p>
		</div>
	</div>
	<p class="why">{reason}</p>
	<p class="safe">Your message is safe in Drafts, and nothing was sent.</p>
	<div class="acts">
		<Button variant="primary" size="lg" block onclick={() => ((open = false), onback?.())}>Go back and edit</Button>
		<Button size="lg" block href="/drafts">Open Drafts</Button>
	</div>
</Sheet>

<style>
	.head {
		display: flex;
		align-items: center;
		gap: var(--sp-14);
	}
	.orb {
		display: grid;
		place-items: center;
		flex: none;
		width: var(--sp-48);
		height: var(--sp-48);
		border-radius: 50%;
		color: var(--danger);
		background: var(--danger-soft);
		border: 1px solid var(--danger-line);
	}
	h2 {
		font: 500 var(--fs-title) var(--font-read);
	}
	.to {
		margin-top: 2px;
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.why {
		margin-top: var(--sp-18);
		font-size: var(--fs-ui-lg);
		line-height: 1.55;
	}
	.safe {
		margin-top: var(--sp-10);
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	.acts {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		margin-top: var(--sp-24);
	}
</style>
