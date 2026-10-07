<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { PenLine, Trash2 } from '#lib/icons.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { confirm } from '#lib/confirm.svelte.js';
	import { formatMessageTime } from '#lib/time.js';
	import { toasts } from '#lib/toast.js';
	import type { DraftSummary } from '#lib/types.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();

	const title = (d: DraftSummary) => d.subject.trim() || '(No subject)';
	const recipients = (d: DraftSummary) => (d.to.length ? d.to.join(', ') : 'No recipients yet');
	const resumeHref = (d: DraftSummary) =>
		`/compose?draft=${encodeURIComponent(d.id)}&account=${encodeURIComponent(d.accountId)}`;

	async function discard(d: DraftSummary) {
		const ok = await confirm.ask({
			title: 'Discard this draft?',
			body: `"${title(d)}" will be removed from Drafts, including the copy on your mail server.`,
			confirmLabel: 'Discard',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api.discardDraft(d.id, d.accountId);
			toasts.push({ text: 'Draft discarded', tone: 'ok' });
			await invalidateAll();
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't discard that draft", tone: 'danger' });
		}
	}
</script>

<TopBar title="Drafts" backHref="/" />

<Page>
	{#if data.drafts.length === 0}
		<div class="empty">
			<span class="orb"><PenLine /></span>
			<h2>Nothing in Drafts</h2>
			<p>Anything you start writing is saved here and on your mail server, so Apple Mail sees it too.</p>
			<Button variant="primary" href="/compose">Start a message</Button>
		</div>
	{:else}
		<ul class="list">
			{#each data.drafts as d (d.id)}
				<li>
					<Glass radius="group">
						<div class="row">
							<a class="main" href={resumeHref(d)}>
								<span class="subj">{title(d)}</span>
								<span class="meta">{recipients(d)} · {formatMessageTime(d.updatedAt)}</span>
								{#if d.source === 'server'}<span class="src">Written in another app</span>{/if}
								{#if d.saveFailed}
									<span class="fail">Not on your mail server yet. Change anything and it tries again.</span>
								{/if}
							</a>
							<button type="button" class="trash" aria-label="Discard {title(d)}" onclick={() => void discard(d)}>
								<Trash2 />
							</button>
						</div>
					</Glass>
				</li>
			{/each}
		</ul>
	{/if}
</Page>

<style>
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
	}
	.main {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-width: 0;
		padding: var(--sp-4) 0;
		color: var(--text);
	}
	.subj {
		overflow: hidden;
		font-size: var(--fs-ui-lg);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.meta {
		margin-top: 2px;
		overflow: hidden;
		font-size: var(--fs-meta);
		color: var(--faint);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.src {
		margin-top: 2px;
		font-size: var(--fs-label);
		color: var(--accent);
	}
	.fail {
		margin-top: 2px;
		font-size: var(--fs-label);
		color: var(--warn);
	}
	.trash {
		display: grid;
		place-items: center;
		flex: none;
		width: var(--hit);
		height: var(--hit);
		border: 0;
		border-radius: 50%;
		background: transparent;
		color: var(--muted);
	}
	.trash :global(svg) {
		width: var(--sp-18);
		height: var(--sp-18);
	}
	.empty {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--sp-10);
		max-width: var(--reading-col);
		padding-top: var(--sp-20);
	}
	.orb {
		display: grid;
		place-items: center;
		width: var(--sp-48);
		height: var(--sp-48);
		border-radius: 50%;
		color: var(--accent);
		background: var(--accent-soft);
	}
	h2 {
		font: 500 var(--fs-title) var(--font-read);
	}
	.empty p {
		color: var(--muted);
	}
</style>
