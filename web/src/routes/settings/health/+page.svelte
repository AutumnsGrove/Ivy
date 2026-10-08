<script lang="ts">
	import { accountAvatar, syncLine } from '#lib/accounts.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import ProgressBar from '#lib/components/ui/ProgressBar.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	const tone = (s: string) => (s === 'ok' ? 'ok' : s === 'syncing' ? 'busy' : 'warn');
</script>

<TopBar title="Mirror health" backHref="/settings" />

<Page>
	<ul class="list">
		{#each data.health.accounts as a (a.id)}
			<li>
				<Glass radius="group" class="acc {tone(a.sync)}">
					<div class="top">
						<Avatar {...accountAvatar(a)} />
						<div class="t">
							<div class="addr">{a.address}</div>
							<div class="st {tone(a.sync)}"><i></i>{syncLine(a)}</div>
						</div>
						{#if a.progress !== undefined}<span class="pct">{Math.round(a.progress * 100)}%</span>{/if}
					</div>
					{#if a.sync === 'auth-failed'}
						<p class="why">The mail server didn't accept the saved password. Your mail on the server is untouched.</p>
						<div class="acts">
							<Button size="sm" variant="tonal" href="/welcome/account?update={a.id}">Update password</Button>
							<Button size="sm" onclick={() => toasts.push({ text: 'Trying again…', tone: 'info' })}>Try again</Button>
							<span class="grow"></span><span class="log">View log</span>
						</div>
					{/if}
					{#if a.progress !== undefined}
						<ProgressBar value={a.progress} label="Reading {a.address}" />
						<p class="why small">You can use this account while it finishes.</p>
					{/if}
				</Glass>
			</li>
		{/each}
		<li>
			<Glass radius="group" class="stats">
				<div class="srow"><span>Search index</span><span class="v">{data.health.searchIndex}</span></div>
				<div class="srow"><span>Meaning search</span><span class="v">{data.health.meaningSearch}</span></div>
				<div class="srow"><span>Storage used</span><span class="v">{data.health.storage}</span></div>
				<div class="srow"><span>Memory in use</span><span class="v">{data.health.memory}</span></div>
			</Glass>
		</li>
	</ul>
</Page>

<style>
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		margin: var(--sp-16) 0 0;
		padding: 0;
		list-style: none;
	}
	.list :global(.acc) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		padding: var(--sp-14) var(--sp-16);
	}
	.list :global(.acc.warn) {
		border-color: var(--warn-line);
	}
	.top {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
	}
	.t {
		flex-grow: 1;
	}
	.addr {
		font-size: var(--fs-ui-lg);
	}
	.st {
		display: flex;
		align-items: center;
		gap: var(--sp-7);
		margin-top: var(--sp-3);
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.st i {
		width: var(--sp-8);
		height: var(--sp-8);
		border-radius: 50%;
		background: var(--ok);
	}
	.st.warn {
		color: var(--warn);
	}
	.st.warn i {
		background: var(--warn);
	}
	.st.busy i {
		background: var(--accent);
	}
	.pct {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.why {
		font-size: var(--fs-small);
		line-height: 1.5;
		color: var(--muted);
	}
	.small {
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.acts {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.grow {
		flex-grow: 1;
	}
	.log {
		font-size: var(--fs-small);
		color: var(--faint);
	}
	.list :global(.stats) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		padding: var(--sp-14) var(--sp-16);
	}
	.srow {
		display: flex;
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.srow span:first-child {
		flex-grow: 1;
	}
	.v {
		color: var(--text);
	}
</style>
