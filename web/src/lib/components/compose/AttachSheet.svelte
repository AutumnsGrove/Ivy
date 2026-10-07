<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { Camera, FileText, FolderOpen, ImageIcon } from '#lib/icons.js';
	import type { MailAttachment } from '#lib/types.js';
	import GroupLabel from '../ui/GroupLabel.svelte';
	import Sheet from '../ui/Sheet.svelte';

	let {
		open = $bindable(),
		accountId,
		photoSize = 'large',
		stripLocation = true,
		onfiles,
		onmail
	}: {
		open: boolean;
		accountId: string;
		photoSize: string;
		stripLocation: boolean;
		onfiles: (files: File[]) => void;
		onmail: (attachment: MailAttachment) => void;
	} = $props();

	let photoInput!: HTMLInputElement;
	let cameraInput!: HTMLInputElement;
	let filesInput!: HTMLInputElement;

	let mail = $state<MailAttachment[]>([]);
	let loaded = $state(false);
	let failed = $state(false);

	// Load "From your mail" on first open, once; a re-open keeps the list.
	$effect(() => {
		if (open && accountId && !loaded) void loadMail();
	});

	async function loadMail() {
		loaded = true;
		try {
			mail = (await api.listMailAttachments(accountId)).attachments;
		} catch {
			failed = true;
		}
	}

	const pick = (input: HTMLInputElement) => {
		input.value = '';
		input.click();
	};
	const chosen = (input: HTMLInputElement) => {
		const files = Array.from(input.files ?? []);
		if (files.length) {
			open = false;
			onfiles(files);
		}
	};

	const SIZE_LABEL: Record<string, string> = { small: 'Small', medium: 'Medium', large: 'Large', original: 'Original' };
	const isImage = (a: MailAttachment) => (a.mime ?? '').startsWith('image/');
	const formatSize = (bytes: number) =>
		bytes >= 1 << 20 ? `${(bytes / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;
</script>

<Sheet bind:open title="Add to message">
	<h2>Add to message</h2>
	<div class="opts">
		<button type="button" class="opt" onclick={() => pick(photoInput)}><ImageIcon />Photos</button>
		<button type="button" class="opt" onclick={() => pick(cameraInput)}><Camera />Camera</button>
		<button type="button" class="opt" onclick={() => pick(filesInput)}><FolderOpen />Files</button>
	</div>
	<input bind:this={photoInput} class="sr" type="file" accept="image/*" multiple aria-label="Photos" onchange={() => chosen(photoInput)} />
	<input bind:this={cameraInput} class="sr" type="file" accept="image/*" capture="environment" aria-label="Camera" onchange={() => chosen(cameraInput)} />
	<input bind:this={filesInput} class="sr" type="file" multiple aria-label="Files" onchange={() => chosen(filesInput)} />

	<GroupLabel>From your mail</GroupLabel>
	{#if mail.length}
		<ul class="recent">
			{#each mail as r (`${r.messageId}:${r.path}`)}
				<li>
					<button
						type="button"
						class="r"
						onclick={() => {
							open = false;
							onmail(r);
						}}
					>
						<span class="th" class:img={isImage(r)}>{#if !isImage(r)}<FileText />{/if}</span>
						<span class="m">
							<span class="ell n">{r.name || 'attachment'}</span>
							<span class="f">{formatSize(r.size)}</span>
						</span>
					</button>
				</li>
			{/each}
		</ul>
	{:else}
		<p class="empty">{failed ? "Couldn't load attachments from your mail." : 'No attachments in your mail yet.'}</p>
	{/if}

	<div class="size">
		<span>
			<span class="t">Photo size</span>
			<span class="s">{stripLocation ? 'Location is removed from photos' : 'Location is kept'}</span>
		</span>
		<span class="v">{SIZE_LABEL[photoSize] ?? 'Large'}</span>
	</div>
</Sheet>

<style>
	h2 {
		margin: 0 var(--sp-4) var(--sp-14);
		font: 300 var(--fs-title-sm) var(--font-ui);
	}
	.opts {
		display: flex;
		gap: var(--sp-10);
	}
	.opt {
		display: flex;
		flex: 1;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--sp-8);
		height: var(--sp-84);
		border-radius: var(--radius-card);
		border: 1px solid var(--glass-border);
		background: var(--wash-soft);
		color: var(--text);
		font: 400 var(--fs-aside) var(--font-ui);
	}
	.opt :global(svg) {
		width: var(--sp-24);
		height: var(--sp-24);
		color: var(--accent);
	}
	/* The file inputs are driven by the buttons above; keep them reachable to
	   assistive tech without showing the native control. */
	.sr {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}
	.empty {
		margin: 0 var(--sp-4);
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.recent {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.r {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		width: 100%;
		padding: var(--sp-8) var(--sp-12) var(--sp-8) var(--sp-8);
		border-radius: var(--radius-md);
		border: 1px solid var(--glass-border);
		background: transparent;
		color: var(--text);
		text-align: left;
	}
	.th {
		display: grid;
		place-items: center;
		flex: none;
		width: var(--sp-44);
		height: var(--sp-44);
		border-radius: var(--sp-10);
		background: var(--accent-soft);
		color: var(--accent);
	}
	.th.img {
		background: var(--attach-a);
	}
	.m {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.n {
		font-size: var(--fs-aside);
	}
	.f {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.size {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin: var(--sp-16) var(--sp-4) 0;
	}
	.size > span:first-child {
		display: flex;
		flex-direction: column;
		flex-grow: 1;
	}
	.t {
		font-size: var(--fs-ui);
	}
	.s {
		margin-top: 2px;
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.v {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
</style>
