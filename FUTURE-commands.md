# Future Commands (Requires Local Machine + Wrangler Login)

These commands need to be run on your local machine with wrangler authenticated.

## Initial Setup

```bash
# Install dependencies
pnpm install

# Login to Cloudflare (if not already)
wrangler login
```

## Database Setup

```bash
# Create the D1 database
wrangler d1 create ivy-db
# Note: Copy the database_id from output and update wrangler.toml

# Create KV namespace for rate limiting
wrangler kv:namespace create IVY_KV
# Note: Copy the id from output and update wrangler.toml

# Run database migrations locally
pnpm db:migrate:local

# Run database migrations on production
pnpm db:migrate
```

## Secrets Setup

```bash
# Set Forward Email API key
wrangler secret put FORWARD_EMAIL_API_KEY

# Set Postmark server token (for newsletters)
wrangler secret put POSTMARK_SERVER_TOKEN

# Set webhook verification secret
wrangler secret put WEBHOOK_SECRET

# Set Turnstile secret key (for contact forms)
wrangler secret put TURNSTILE_SECRET_KEY

# Set IP hash salt (for privacy-preserving rate limiting)
wrangler secret put IP_HASH_SALT
```

## Development

```bash
# Run development server with local D1/R2/KV
wrangler dev --local

# Or use SvelteKit dev server (no Cloudflare bindings)
pnpm dev
```

## Testing

```bash
# Run all tests
pnpm test

# Run unit tests only
pnpm test:unit

# Run integration tests only
pnpm test:integration

# Run tests with coverage
pnpm test -- --coverage
```

## Deployment

```bash
# Build for production
pnpm build

# Deploy to Cloudflare Workers
pnpm deploy
```

## R2 Storage Setup

```bash
# Create R2 bucket for encrypted email storage
wrangler r2 bucket create ivy-storage
```

---

*Commands will be updated as development progresses.*
