<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { connectErrorText } from '#lib/connect.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Field from '#lib/components/ui/Field.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	// Set when the screen was opened to fix one account's password.
	const updating = $derived(data.updating);

	let email = $state('');
	let password = $state('');
	let smart = $state(false);
	let busy = $state(false);
	let failure = $state('');
	const addressOk = $derived(/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email));
	const ready = $derived(!busy && password !== '' && (updating !== null || addressOk));

	// The server tests the login before it stores anything, so a failure leaves
	// what was typed in place for another try and nothing behind on the server.
	async function submit(event: SubmitEvent) {
		event.preventDefault();
		if (!ready) return;
		busy = true;
		failure = '';
		try {
			if (updating) {
				await api.updateAccountPassword(updating.id, password);
				toasts.push({ text: 'Password updated. Ivy is signing in again.', tone: 'info' });
				password = '';
				await goto('/settings/health');
			} else {
				await api.connectAccount(email.trim(), password, smart);
				toasts.push({ text: 'Connected. Reading your mailbox, newest first.', tone: 'info' });
				password = '';
				await goto('/');
			}
		} catch (e) {
			failure = connectErrorText(e);
		} finally {
			busy = false;
		}
	}
</script>

<TopBar backHref={updating ? '/settings/health' : '/welcome'}>
	{#snippet trailing()}{#if !updating}<span class="step">Step 1 of 2</span>{/if}{/snippet}
</TopBar>

<Page>
	<form onsubmit={submit}>
		{#if updating}
			<h1>Update password</h1>
			<p class="lede">Enter the new password for {updating.address}. Ivy tests it first and keeps the old one if it doesn't work.</p>
		{:else}
			<h1>Connect an account</h1>
			<p class="lede">Ivy keeps a private copy of your mail so search is instant. Your provider stays the source of truth.</p>

			<div class="f">
				<Field label="Email address" type="email" autocomplete="username" bind:value={email} placeholder="you@yourdomain.com" />
				<p class="provider">For now Ivy connects to Purelymail. Other providers come later.</p>
			</div>
		{/if}
		<div class="f">
			<Field
				label="App password"
				type="password"
				autocomplete="current-password"
				bind:value={password}
				hint="Use an app password if Purelymail offers one. It is stored on this server only, in a private file, and never leaves it except to sign in."
			/>
		</div>

		{#if !updating}
			<Glass radius="group" class="smart">
				<div class="st">
					<div>Smart features</div>
					<div class="sub">
						{#if smart}
							Search by meaning sends message text to OpenRouter, within your monthly cap. It starts the next time Ivy starts.
						{:else}
							Off unless you turn it on. When off, this account's mail never leaves your server.
						{/if}
					</div>
				</div>
				<Toggle label="Smart features" bind:checked={smart} />
			</Glass>

			<p class="note"><Glint />Ivy will read your whole mailbox once, newest first. You can start using it right away.</p>
		{/if}

		{#if failure}<p class="fail" role="alert">{failure}</p>{/if}

		<div class="cta">
			<Button variant="primary" size="xl" block type="submit" disabled={!ready}>
				{#if busy}Testing…{:else if updating}Test and save{:else}Test and connect{/if}
			</Button>
		</div>
	</form>
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
	.provider {
		margin: var(--sp-10) var(--sp-4) 0;
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.fail {
		margin: var(--sp-22) 0 0;
		padding: var(--sp-12) var(--sp-16);
		border: 1px solid var(--danger-line);
		border-radius: var(--sp-16);
		background: var(--danger-soft);
		font-size: var(--fs-small);
		line-height: 1.5;
		color: var(--danger);
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
