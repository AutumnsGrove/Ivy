import type { Page } from '@playwright/test';

/** A visible toast containing `text`. The only place that knows how toasts are rendered. */
export const toast = (page: Page, text: string) => page.locator('[data-sonner-toast]').filter({ hasText: text });
