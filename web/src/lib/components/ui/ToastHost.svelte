<script lang="ts">
	import { toasts } from '#lib/toast.svelte.js';
	import { CircleCheck, TriangleAlert, CircleAlert } from '#lib/icons.js';
	import Glint from './Glint.svelte';
</script>

<div class="host" role="status" aria-live="polite">
	{#each toasts.items as t (t.id)}
		<div class="toast {t.tone}">
			{#if t.tone === 'info'}
				<Glint />
			{:else if t.tone === 'ok'}
				<span class="icon ok"><CircleCheck /></span>
			{:else if t.tone === 'warn'}
				<span class="icon warn"><TriangleAlert /></span>
			{:else if t.tone === 'danger'}
				<span class="icon danger"><CircleAlert /></span>
			{/if}
			<div class="copy">
				<div>{t.text}</div>
				{#if t.detail}<div class="detail">{t.detail}</div>{/if}
			</div>
			{#if t.action}
				<button type="button" class="act" onclick={() => toasts.act(t.id)}>{t.action.label}</button>
			{/if}
		</div>
	{/each}
</div>

<style>
	.host {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-58) + var(--sp-28) + env(safe-area-inset-bottom));
		z-index: var(--z-toast);
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		width: min(calc(100% - var(--sp-28)), var(--phone-max));
		transform: translateX(-50%);
		pointer-events: none;
	}
	.toast {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		min-height: var(--sp-56);
		padding: var(--sp-8) var(--sp-10) var(--sp-8) var(--sp-16);
		border-radius: var(--sp-28);
		border: 1px solid var(--glass-border);
		background: var(--toast-bg);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
		box-shadow: var(--shadow-toast);
		font-size: var(--fs-ui);
		pointer-events: auto;
	}
	.warn {
		border-color: var(--warn-line);
	}
	.danger {
		border-color: var(--danger-line);
	}
	.copy {
		flex-grow: 1;
		min-width: 0;
	}
	.detail {
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.icon {
		display: flex;
	}
	.ok {
		color: var(--ok);
	}
	.icon.warn {
		color: var(--warn);
	}
	.icon.danger {
		color: var(--danger);
	}
	.act {
		height: var(--sp-36);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-18);
		border: 0;
		background: var(--accent-soft);
		color: var(--accent);
		font: 500 var(--fs-aside) var(--font-ui);
	}
</style>
