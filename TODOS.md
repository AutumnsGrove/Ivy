# Ivy TODOs

## Phase 1: Foundation (MVP)

### Project Setup
- [x] Initialize SvelteKit project with TypeScript
- [x] Configure Cloudflare adapter (`@sveltejs/adapter-cloudflare`)
- [x] Set up `wrangler.toml` with D1, R2, and KV bindings
- [x] Configure pnpm workspace and dependencies
- [x] Set up Vitest for testing
- [x] Create secrets template (`secrets_template.json`)

### Database (D1)
- [x] Create D1 database via Wrangler
- [x] Write migration: `ivy_settings` table
- [x] Write migration: `ivy_emails` table
- [x] Write migration: `ivy_email_queue` table
- [x] Write migration: `ivy_webhook_buffer` table
- [x] Write migration: `ivy_contact_form_buffer` table
- [x] Write migration: `ivy_newsletter_sends` table
- [x] Write migration: `ivy_admin_audit_log` table
- [x] Create indexes for all tables

### Encryption (lib/crypto)
- [x] Implement AES-256-GCM encrypt/decrypt utilities
- [x] Implement PBKDF2 key derivation (wrapper key from credentials)
- [x] Implement email key generation and storage
- [x] Implement recovery phrase generation (BIP39 24-word)
- [x] Write encryption round-trip tests (26 tests passing)

### Forward Email Integration
- [x] Create Forward Email API client (`lib/api/forwardEmail.ts`)
- [x] Implement webhook signature verification (HMAC-SHA256)
- [x] Implement constant-time comparison for signatures
- [x] Implement webhook rate limiting (KV-based)
- [x] Implement webhook payload validation
- [x] Create send email function (SMTP via API)
- [ ] Write integration tests with mocked responses
- [ ] **NEXT: Set Forward Email API key and webhook secret**

### Webhook Handler (workers/webhook)
- [x] Create webhook endpoint (`/api/webhook/incoming`)
- [x] Implement buffer-first pattern (immediate D1 write)
- [x] Implement async buffer processing
- [x] Implement retry logic with exponential backoff
- [x] Implement dead letter handling
- [ ] Alert user on delivery failures
- [ ] Implement proper MIME email parsing (currently simplified)
- [ ] **NEXT: Configure webhook URL in Forward Email dashboard**

### Core UI Components
- [x] Create layout with sidebar navigation
- [x] Create email list component (ThreadList)
- [x] Create thread view component (collapsible messages, reply box)
- [x] Create compose modal
- [ ] Create rich text editor (Tiptap or similar) — *using textarea for now*
- [ ] Create attachment upload component

### Design System
- [x] Grove-themed color palette (dark mode default)
- [x] Light mode support with theme toggle
- [x] CSS custom properties for theming
- [x] Icon component with SVG icons
- [x] Typography and spacing system

### Core Routes (Fully Styled)
- [x] `/` — Redirect to inbox (server-side)
- [x] `/inbox` — Main inbox view with email list
- [x] `/sent` — Sent emails with empty state
- [x] `/drafts` — Draft emails with mock draft
- [x] `/archive` — Archived threads with empty state
- [x] `/trash` — Deleted items with 30-day notice
- [x] `/thread/[id]` — Thread detail with message expansion
- [x] `/compose` — Compose modal (accessible from anywhere)
- [x] `/settings` — Full settings UI (account, appearance, email, notifications, security, storage)

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

## Current Sprint (December 26, 2025)

### ✅ COMPLETED TODAY:
- Forward Email API client with HMAC-SHA256 signature verification
- Webhook handler with DoS-resistant rate limiting
- Buffer-first email processing (reliability)
- Retry logic with exponential backoff
- Dead letter queue for failed emails
- Email threading (Gmail-style)

### 🚧 IN PROGRESS:
- Setting up Forward Email credentials and secrets
- Configuring webhook URL in Forward Email dashboard

### 🎯 NEXT UP:
1. **Set secrets:**
   - `FORWARD_EMAIL_API_KEY` (from Forward Email dashboard)
   - `WEBHOOK_SECRET` (generate with `openssl rand -base64 32`)
2. **Configure webhook URL:** Point Forward Email to deployed Ivy webhook endpoint
3. **Implement queue processor:** For delayed send/unsend feature
4. **Postmark integration:** Newsletter sending for Oak+ users
5. **Heartwood OAuth:** Session validation with Durable Objects

### 📝 NOTES:
- Forward Email setup COMPLETE (domain verified, SMTP working, tested on iPhone Mail)
- Webhook signature key visible in screenshots (WILL BE RESET before production)
- Email encryption currently using placeholder (NOT zero-knowledge yet - client-side key handling needed)
- MIME parsing is simplified - production needs proper multipart parsing library

---

*Last updated: 2025-12-26*
