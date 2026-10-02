<script lang="ts">
	import { Camera, FileText, FolderOpen, ImageIcon } from '#lib/icons.js';
	import GroupLabel from '../ui/GroupLabel.svelte';
	import Sheet from '../ui/Sheet.svelte';

	let { open = $bindable(), onpick }: { open: boolean; onpick: (name: string) => void } = $props();

	const sources = [
		{ label: 'Photos', icon: ImageIcon },
		{ label: 'Camera', icon: Camera },
		{ label: 'Files', icon: FolderOpen }
	];
	const recent = [
		{ name: 'blog-home.png', from: 'Mara Linden · today', image: true },
		{ name: 'migration.pdf', from: 'You sent · last week', image: false },
		{ name: 'invoice-0412.pdf', from: 'Cloudflare · Oct', image: false }
	];
</script>

<Sheet bind:open title="Add to message">
	<h2>Add to message</h2>
	<div class="opts">
		{#each sources as s (s.label)}
			<button type="button" class="opt" onclick={() => ((open = false), onpick(`${s.label.toLowerCase()}.jpg`))}>
				<s.icon />{s.label}
			</button>
		{/each}
	</div>

	<GroupLabel>From your mail</GroupLabel>
	<ul class="recent">
		{#each recent as r (r.name)}
			<li>
				<button type="button" class="r" onclick={() => ((open = false), onpick(r.name))}>
					<span class="th" class:img={r.image}>{#if !r.image}<FileText />{/if}</span>
					<span class="m"><span class="ell n">{r.name}</span><span class="f">{r.from}</span></span>
				</button>
			</li>
		{/each}
	</ul>

	<div class="size"><span><span class="t">Photo size</span><span class="s">Location is removed from photos</span></span><span class="v">Large</span></div>
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
