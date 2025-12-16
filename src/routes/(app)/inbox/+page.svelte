<script lang="ts">
	import { threads, selectedThreadId } from '$lib/stores';
	import Icon from '$lib/components/Icons.svelte';
	import { goto } from '$app/navigation';

	function formatDate(date: Date): string {
		const now = new Date();
		const diff = now.getTime() - date.getTime();
		const days = Math.floor(diff / (1000 * 60 * 60 * 24));

		if (days === 0) {
			return date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
		} else if (days === 1) {
			return 'Yesterday';
		} else if (days < 7) {
			return date.toLocaleDateString('en-US', { weekday: 'short' });
		} else {
			return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
		}
	}

	function selectThread(id: string) {
		selectedThreadId.set(id);
		goto(`/thread/${id}`);
	}

	function getSnippet(thread: typeof $threads[0]): string {
		// Mock snippets for demo
		const snippets: Record<string, string> = {
			'1': 'Welcome to your new Grove email. We\'re excited to have you join our community...',
			'2': 'Your @grove.place email address is now active. You can start sending and receiving...',
			'3': 'I think we should move forward with option B. It aligns better with our goals for...',
			'4': 'Here are the key takeaways from our meeting: 1. Timeline adjusted to March...',
			'5': 'Please find attached your invoice for December 2024. Payment is due within 30...',
			'6': 'This week in Grove: New features, community highlights, and upcoming events...'
		};
		return snippets[thread.id] || 'No preview available';
	}
</script>

<svelte:head>
	<title>Inbox - Ivy</title>
</svelte:head>

<div class="inbox">
	<header class="inbox-header">
		<div class="header-left">
			<h1>Inbox</h1>
			<span class="unread-count">{$threads.filter(t => !t.isRead).length} unread</span>
		</div>
		<div class="header-actions">
			<button class="action-btn" title="Refresh">
				<Icon name="inbox" size={18} />
			</button>
		</div>
	</header>

	<div class="thread-list">
		{#each $threads as thread (thread.id)}
			<button
				class="thread-item"
				class:unread={!thread.isRead}
				class:selected={$selectedThreadId === thread.id}
				onclick={() => selectThread(thread.id)}
			>
				<div class="thread-checkbox">
					<input type="checkbox" onclick={(e) => e.stopPropagation()} />
				</div>

				<div class="thread-star">
					<button class="star-btn" onclick={(e) => e.stopPropagation()} title="Star">
						<Icon name="star" size={16} />
					</button>
				</div>

				<div class="thread-content">
					<div class="thread-top">
						<span class="thread-from">
							{thread.participants.filter(p => p !== 'you').slice(0, 2).join(', ')}
							{#if thread.participants.length > 3}
								<span class="participant-count">+{thread.participants.length - 2}</span>
							{/if}
						</span>
						<span class="thread-date">{formatDate(thread.lastDate)}</span>
					</div>

					<div class="thread-middle">
						<span class="thread-subject">{thread.subject}</span>
					</div>

					<div class="thread-bottom">
						<span class="thread-snippet">{getSnippet(thread)}</span>
					</div>
				</div>
			</button>
		{:else}
			<div class="empty-state">
				<div class="empty-icon">
					<Icon name="inbox" size={48} />
				</div>
				<h2>Your inbox is empty</h2>
				<p>Messages you receive will appear here</p>
			</div>
		{/each}
	</div>
</div>

<style>
	.inbox {
		height: 100%;
		display: flex;
		flex-direction: column;
	}

	.inbox-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-4) var(--space-6);
		border-bottom: 1px solid var(--color-border);
		background: var(--color-bg-secondary);
	}

	.header-left {
		display: flex;
		align-items: baseline;
		gap: var(--space-3);
	}

	.inbox-header h1 {
		font-size: var(--text-xl);
		font-weight: var(--font-semibold);
		color: var(--color-text-primary);
	}

	.unread-count {
		font-size: var(--text-sm);
		color: var(--color-text-tertiary);
	}

	.header-actions {
		display: flex;
		gap: var(--space-2);
	}

	.action-btn {
		padding: var(--space-2);
		border-radius: var(--radius-md);
		color: var(--color-text-secondary);
		transition: all var(--transition-fast);
	}

	.action-btn:hover {
		background: var(--color-surface-hover);
		color: var(--color-text-primary);
	}

	.thread-list {
		flex: 1;
		overflow-y: auto;
	}

	.thread-item {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		width: 100%;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--color-border-subtle);
		cursor: pointer;
		transition: background var(--transition-fast);
		text-align: left;
		background: transparent;
	}

	.thread-item:hover {
		background: var(--color-surface-hover);
	}

	.thread-item.selected {
		background: var(--color-primary-muted);
	}

	.thread-item.unread {
		background: var(--color-bg-tertiary);
	}

	.thread-item.unread:hover {
		background: var(--color-surface-hover);
	}

	.thread-checkbox {
		padding-top: var(--space-1);
	}

	.thread-checkbox input {
		width: 16px;
		height: 16px;
		accent-color: var(--color-primary);
		cursor: pointer;
	}

	.thread-star {
		padding-top: var(--space-1);
	}

	.star-btn {
		padding: 2px;
		border-radius: var(--radius-sm);
		color: var(--color-text-muted);
		transition: all var(--transition-fast);
	}

	.star-btn:hover {
		color: var(--color-warning);
	}

	.thread-content {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	.thread-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.thread-from {
		font-weight: var(--font-medium);
		color: var(--color-text-primary);
		font-size: var(--text-sm);
	}

	.thread-item.unread .thread-from {
		font-weight: var(--font-semibold);
	}

	.participant-count {
		color: var(--color-text-tertiary);
		font-weight: var(--font-normal);
	}

	.thread-date {
		font-size: var(--text-xs);
		color: var(--color-text-tertiary);
		white-space: nowrap;
	}

	.thread-item.unread .thread-date {
		color: var(--color-primary);
		font-weight: var(--font-medium);
	}

	.thread-middle {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}

	.thread-subject {
		font-size: var(--text-sm);
		color: var(--color-text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.thread-item.unread .thread-subject {
		font-weight: var(--font-medium);
	}

	.thread-bottom {
		display: flex;
	}

	.thread-snippet {
		font-size: var(--text-sm);
		color: var(--color-text-tertiary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Empty State */
	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: var(--space-16);
		text-align: center;
	}

	.empty-icon {
		color: var(--color-text-muted);
		margin-bottom: var(--space-4);
	}

	.empty-state h2 {
		font-size: var(--text-lg);
		font-weight: var(--font-medium);
		color: var(--color-text-secondary);
		margin-bottom: var(--space-2);
	}

	.empty-state p {
		font-size: var(--text-sm);
		color: var(--color-text-tertiary);
	}
</style>
