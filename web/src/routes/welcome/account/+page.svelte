<script lang="ts">
	import { goto } from '$app/navigation';
	import { Check } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Field from '#lib/components/ui/Field.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.svelte.js';

	let email = $state('');
	let password = $state('');
	let smart = $state(false);
	const found = $derived(/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email));

	function connect() {
		toasts.push({ text: 'Connected. Reading your mailbox, newest first.', tone: 'info' });
		void goto('/');
	}
</script>

<TopBar backHref="/welcome">
	{#snippet trailing()}<span class="step">Step 1 of 2</span>{/snippet}
</TopBar>

<Page>
	<h1>Connect an account</h1>
	<p class="lede">Ivy keeps a private copy of your mail so search is instant. Your provider stays the source of truth.</p>

	<div class="f">
		<Field label="Email address" type="email" bind:value={email} placeholder="you@example.com" />
		{#if found}<p class="found"><Check />Found your mail server, settings filled in</p>{/if}
	</div>
	<div class="f">
		<Field
			label="App password"
			type="password"
			bind:value={password}
			hint="Use an app-specific password if your provider offers one. It is stored on this server only."
		/>
	</div>

	<Glass radius="group" class="smart">
		<div class="st">
			<div>Smart features</div>
			<div class="sub">Off unless you turn it on. When off, this account's mail never leaves your server.</div>
		</div>
		<Toggle label="Smart features" bind:checked={smart} />
	</Glass>

	<p class="note"><Glint />Ivy will read your whole mailbox once, newest first. You can start using it right away.</p>

	<div class="cta"><Button variant="primary" size="xl" block disabled={!found || !password} onclick={connect}>Test and connect</Button></div>
</Page>

<style>
	.step {
		padding-right: var(--sp-6);
		font-size: var(--fs-small);
		color: var(--faint);
	}
	h1 {
		margin-top: var(--sp-16);
		font: 500 var(--fs-read-title-lg) / 1.15 var(--font-read);
	}
	.lede {
		margin-top: var(--sp-8);
		font-size: var(--fs-ui);
		line-height: 1.5;
		color: var(--muted);
	}
	.f {
		margin-top: var(--sp-22);
	}
	.found {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		margin: var(--sp-10) var(--sp-4) 0;
		font-size: var(--fs-aside);
		color: var(--accent);
	}
	.found :global(svg) {
		width: var(--sp-16);
		height: var(--sp-16);
	}
	:global(.smart) {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		margin-top: var(--sp-24);
		padding: var(--sp-14) var(--sp-16);
	}
	.st {
		flex-grow: 1;
		font-size: var(--fs-ui-lg);
	}
	.sub {
		margin-top: 2px;
		font-size: var(--fs-note);
		line-height: 1.45;
		color: var(--faint);
	}
	.note {
		display: flex;
		align-items: flex-start;
		gap: var(--sp-10);
		margin: var(--sp-22) var(--sp-4) 0;
		font-size: var(--fs-small);
		line-height: 1.5;
		color: var(--muted);
	}
	.note :global(.glint) {
		margin-top: var(--sp-6);
	}
	.cta {
		margin-top: var(--sp-32);
	}
</style>
