# Ivy TODOs

## Phase 1: Foundation (MVP)

### Project Setup
- [ ] Initialize SvelteKit project with TypeScript
- [ ] Configure Cloudflare adapter (`@sveltejs/adapter-cloudflare`)
- [ ] Set up `wrangler.toml` with D1, R2, and KV bindings
- [ ] Configure pnpm workspace and dependencies
- [ ] Set up Vitest for testing
- [ ] Create secrets template (`secrets_template.json`)

### Database (D1)
- [ ] Create D1 database via Wrangler
- [ ] Write migration: `ivy_settings` table
- [ ] Write migration: `ivy_emails` table
- [ ] Write migration: `ivy_email_queue` table
- [ ] Write migration: `ivy_webhook_buffer` table
- [ ] Write migration: `ivy_contact_form_buffer` table
- [ ] Write migration: `ivy_newsletter_sends` table
- [ ] Write migration: `ivy_admin_audit_log` table
- [ ] Create indexes for all tables

### Encryption (lib/crypto)
- [ ] Implement AES-256-GCM encrypt/decrypt utilities
- [ ] Implement Argon2id key derivation (wrapper key from credentials)
- [ ] Implement email key generation and storage
- [ ] Implement recovery phrase generation (BIP39 24-word)
- [ ] Write encryption round-trip tests

### Forward Email Integration
- [ ] Create Forward Email API client (`lib/api/forwardEmail.ts`)
- [ ] Implement webhook signature verification (HMAC-SHA256)
- [ ] Implement constant-time comparison for signatures
- [ ] Implement webhook rate limiting (KV-based)
- [ ] Implement webhook payload validation
- [ ] Create send email function (SMTP via API)
- [ ] Write integration tests with mocked responses

### Webhook Handler (workers/webhook)
- [ ] Create webhook endpoint (`/api/webhook/incoming`)
- [ ] Implement buffer-first pattern (immediate D1 write)
- [ ] Implement async buffer processing
- [ ] Implement retry logic with exponential backoff
- [ ] Implement dead letter handling
- [ ] Alert user on delivery failures

### Core UI Components
- [ ] Create layout with sidebar navigation
- [ ] Create email list component
- [ ] Create thread view component
- [ ] Create compose modal
- [ ] Create rich text editor (Tiptap or similar)
- [ ] Create attachment upload component

### Core Routes
- [ ] `/` — Redirect to inbox or onboarding
- [ ] `/inbox` — Main inbox view
- [ ] `/sent` — Sent emails
- [ ] `/drafts` — Draft emails
- [ ] `/archive` — Archived threads
- [ ] `/trash` — Deleted items
- [ ] `/thread/[id]` — Thread detail view
- [ ] `/compose` — Full-page compose (mobile)
- [ ] `/settings` — User settings

### Unsend Queue
- [ ] Implement delayed send queue (D1)
- [ ] Create queue processor worker
- [ ] Implement cancel/unsend endpoint
- [ ] Handle race conditions (optimistic locking)
- [ ] Show unsend timer in UI

## Phase 2: Organization

### Labels
- [ ] Create label CRUD endpoints
- [ ] Store labels in encrypted envelope
- [ ] Create label management UI
- [ ] Implement label filtering in inbox

### Search (Client-Side)
- [ ] Set up IndexedDB schema
- [ ] Build search index from decrypted emails
- [ ] Implement full-text search
- [ ] Create search UI component

### Contact Form Integration
- [ ] Create contact form submission endpoint
- [ ] Implement Turnstile verification
- [ ] Implement IP-based rate limiting
- [ ] Convert submissions to email threads
- [ ] Add "Contact Form" label automatically

## Phase 3: Newsletters (Oak+)

### Postmark Integration
- [ ] Create Postmark API client (`lib/api/postmark.ts`)
- [ ] Implement broadcast stream sending
- [ ] Track newsletter sends in D1
- [ ] Enforce tier-based weekly limits

### Newsletter UI
- [ ] Create "Send to Subscribers" option in compose
- [ ] Show subscriber count before send
- [ ] Add automatic unsubscribe link
- [ ] Store copy in user's Ivy (encrypted)

## Phase 4: Polish

### Security Hardening
- [ ] Implement CSP headers
- [ ] Set up DOMPurify for HTML email rendering
- [ ] Create sandboxed iframe for email display
- [ ] Audit all endpoints for auth checks

### Mobile Optimization
- [ ] Responsive layout adjustments
- [ ] Touch-friendly targets
- [ ] Swipe actions (archive, delete)
- [ ] Pull-to-refresh

### PWA
- [ ] Create manifest.json
- [ ] Set up service worker
- [ ] Implement offline email viewing
- [ ] Add "Add to Home Screen" prompt

## Backlog (Future)

- [ ] Folders (hierarchical organization)
- [ ] Filters/rules (auto-apply labels)
- [ ] Starred/flagged messages
- [ ] Keyboard shortcuts
- [ ] Import from other providers
- [ ] Email templates
- [ ] Vacation responder
- [ ] Blocked senders list
- [ ] Calendar integration (ICS detection)
- [ ] Admin tools expansion

---

*Last updated: 2025-12-15*
