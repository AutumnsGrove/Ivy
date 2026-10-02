<script lang="ts">
	import { Toaster } from 'svelte-sonner';
</script>

<!-- Sonner draws the toasts; every colour, radius and offset below comes from our tokens.
     Selectors are prefixed with `body` so they out-rank sonner's own theme rules, which would
     otherwise win and paint its default black card. -->
<Toaster
	position="bottom-center"
	theme="dark"
	visibleToasts={3}
	offset={{ bottom: 'calc(var(--sp-90) + var(--sp-14))' }}
	mobileOffset={{ bottom: 'calc(var(--sp-90) + var(--sp-14))', left: 'var(--sp-14)', right: 'var(--sp-14)' }}
	toastOptions={{ classes: { actionButton: 'ivy-toast-action' } }}
/>

<style>
	:global(body [data-sonner-toaster][data-sonner-theme]) {
		/* sonner writes --width inline (356px); only !important beats an inline declaration */
		--width: min(calc(100vw - var(--sp-28)), calc(var(--phone-max) - var(--sp-28))) !important;
		--border-radius: var(--sp-28);
		--normal-bg: var(--toast-bg);
		--normal-border: var(--glass-border);
		--normal-text: var(--text);
		--success-bg: var(--toast-bg);
		--success-border: var(--glass-border);
		--success-text: var(--text);
		--info-bg: var(--toast-bg);
		--info-border: var(--glass-border);
		--info-text: var(--text);
		--warning-bg: var(--toast-bg);
		--warning-border: var(--warn-line);
		--warning-text: var(--text);
		--error-bg: var(--toast-bg);
		--error-border: var(--danger-line);
		--error-text: var(--text);
		font-family: var(--font-ui);
	}
	:global(body [data-sonner-toast][data-styled='true']) {
		min-height: var(--sp-56);
		padding: var(--sp-8) var(--sp-10) var(--sp-8) var(--sp-16);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
		box-shadow: var(--shadow-toast);
		font-size: var(--fs-ui);
	}
	:global(body [data-sonner-toast] [data-description]) {
		font-size: var(--fs-note);
		color: var(--muted);
	}
	:global(body [data-sonner-toast][data-type='success'] [data-icon]) {
		color: var(--ok);
	}
	:global(body [data-sonner-toast][data-type='warning'] [data-icon]) {
		color: var(--warn);
	}
	:global(body [data-sonner-toast][data-type='error'] [data-icon]) {
		color: var(--danger);
	}
	:global(body [data-sonner-toast][data-type='info'] [data-icon]) {
		color: var(--firefly);
	}
	:global(body [data-sonner-toast] [data-button].ivy-toast-action) {
		height: var(--sp-36);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-18);
		background: var(--accent-soft);
		color: var(--accent);
		font: 500 var(--fs-aside) var(--font-ui);
	}
</style>
