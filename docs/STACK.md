# Stack: libraries and tools

Status: DRAFT (2026-10-01). **Every version and API below is unverified until a spike pins it** (the
rule from `PLAN.md` section 6: verify before pinning). The constraint: **pure Go, no cgo**
(`CGO_ENABLED=0` everywhere). "Settled" items come from the Q&A log; the rest are proposals.

## Platform and hardware notes

- **Target:** Libre Computer "Le Potato" (AML-S905X-CC): Amlogic S905X, quad Cortex-A53 at 1.5 GHz
  (aarch64), 2 GB DDR3, microSD plus eMMC socket, 100 Mbit Ethernet
  ([vendor page](https://libre.computer/products/aml-s905x-cc/)). Here: ~1.9 GB usable, ~800 MB free.
  Slow cores and slow flash, so: few allocations, batched writes, precomputed/compressed assets,
  no heavy work on the request path. Cross-compile with `GOOS=linux GOARCH=arm64` for CI checks;
  real timing is on the device.
- **Dev machine:** anything; CI is GitHub Actions. Playwright's Chromium is the only non-Go runtime
  in test (dev/CI only, never on the potato).

## Go backend

| Need | Choice | Why / notes |
|---|---|---|
| HTTP, routing | stdlib `net/http` (pattern mux) | No framework. SSE is a plain handler. |
| CLI | `spf13/cobra` | `run`, `init`, `update`, `backup`, `restore`, `doctor`. Thin: handlers call packages. |
| Config | YAML library (`goccy/go-yaml`, verify maintenance) + `joho/godotenv` | `ivy.yaml` + `.env`; archived `yaml.v3` is avoided. |
| Logging | `log/slog` | Structured; JSON in prod, text in dev. |
| SQLite | `modernc.org/sqlite` (**settled**) | Pure Go, WAL, FTS5 (verify). Plain `database/sql`; hand-rolled positional migrations. |
| SQL typing | `sqlc` (proposal) | Generates typed Go from `.sql` files, build-time tool only, no runtime dependency. See qa-log round 21. |
| IMAP client | `emersion/go-imap/v2` (beta; verify API) | Has CONDSTORE/QRESYNC/IDLE/MOVE. **Test server:** its `imapserver` + `imapmemserver`. Wrapped in our `imap/` package. |
| SMTP / SASL | `emersion/go-smtp`, `emersion/go-sasl` | Client for sending; server for the mail world. |
| MIME parse/build | `jhillyerd/enmime` (**settled**) | Parse and build. Plus `emersion/go-message` where lower-level access is needed. |
| HTML sanitise | `microcosm-cc/bluemonday` (**settled**) | Strict policy; `golang.org/x/net/html` for text extraction/tokenising. |
| Compression | `klauspost/compress` (zstd, gzip/flate, `gzhttp`) + `andybalholm/brotli` | All pure Go. Details in `PERFORMANCE.md`. Also zstd for `raw_blob` at rest. |
| Embeddings | Ollama HTTP API (**settled**) behind an `Embedder` interface | Remote OpenAI-compatible provider optional. |
| LLM calls | stdlib `net/http` client in `llm/` and `jev/` | One gate, one chokepoint (**settled**). No vendor SDK needed. |
| Attachment text | `golang.org/x/net/html`, `archive/zip` + `encoding/xml` (OOXML), a pure-Go PDF text-layer library (spike, quality uncertain) | PDF library choice is a Milestone 2 spike. |
| Images | stdlib `image/*`, `golang.org/x/image` | Downscale and EXIF strip in pure Go. **HEIC is the open C-free question:** iOS Safari normally hands back JPEG for `accept="image/*"`; verify on the real phone before adding any decoder (a WASM-based one via `wazero` is the pure-Go fallback). |
| Process mgmt | `golang.org/x/sync/errgroup` | Goroutine ownership and shutdown. |
| Frontend embedding | `go:embed` of `web/build` with precompressed variants | Committed build output (**settled**). |

## Go testing and quality tools

| Tool | Use |
|---|---|
| stdlib `testing` + `google/go-cmp` | Assertions via `cmp.Diff`; no assertion frameworks or mock generators. |
| `pgregory.net/rapid` | Property and model-based tests (sync convergence, threading). |
| Go native fuzzing | Sanitizer, MIME, header parsing, rule conditions. |
| `go.uber.org/goleak` | Goroutine leak detection per package. |
| `golang.org/x/perf/cmd/benchstat` | Statistical benchmark comparison across commits. |
| `internal/mailworld` + `cmd/ivy-dev` | The fake mail universe and its CLI (`STANDARDS.md` section 3). |
| `gofumpt`, `staticcheck`, `golangci-lint`, `govulncheck` | Format, lint, vulnerabilities. |
| `go build -ldflags` + `bloaty`-style size check | Binary size budget (see `PERFORMANCE.md`). |

## Frontend

| Need | Choice |
|---|---|
| Framework | SvelteKit, Svelte 5 (runes), `adapter-static`, TypeScript strict (**settled**: Svelte, static, pnpm, pure CSS) |
| Styling | Pure CSS custom properties, vendored Grove tokens; Lexend + Newsreader self-hosted (subset, `font-display: swap`, preloaded); Lucide icons imported per icon |
| Unit/component | Vitest + `@testing-library/svelte` |
| E2E / visual / a11y | `@playwright/test`, screenshot diffs, `@axe-core/playwright` |
| Lint/format | ESLint, Prettier, `svelte-check` |
| Build | Vite (via SvelteKit); a post-build step precompresses assets (brotli/zstd/gzip) |
| Sanitised mail view | Sandboxed iframe fed by server-sanitised HTML (no client sanitiser library) |

Pinned in `web/package.json` (2026-10-02, all dev dependencies; the shipped bundle is Svelte's
compiled output plus fonts and icons, about 100 KB gzip for every route together):

| Package | Why |
|---|---|
| `@sveltejs/kit` 3, `svelte` 5, `vite` 8, `@sveltejs/adapter-static` | The framework and static output embedded by the Go binary |
| `@lucide/svelte` | Icons, imported per icon through `src/lib/icons.ts` (the old `lucide-svelte` is deprecated) |
| `@fontsource-variable/lexend`, `@fontsource-variable/newsreader` | Self-hosted fonts, bundled and hashed by Vite (no third-party font requests) |
| `svelte-sonner` 1.x | Toasts (MIT, Svelte 5 only; `svelte-french-toast` is stuck on Svelte 3/4). Wrapped by `src/lib/toast.ts` so call sites never import it, and styled from our tokens in `Toaster.svelte` |
| `cookie` | Kit's own runtime dependency; declared directly so the built server chunk resolves the right copy under pnpm's strict layout |
| `vitest`, `jsdom`, `@testing-library/svelte`, `@testing-library/jest-dom` | Unit and component tests |
| `@playwright/test` | E2E on WebKit (iPhone) and Chromium (desktop) |

Frontend dependency policy: the runtime dependency list should stay near-empty (Svelte plus icons).
Anything else is justified here with its compressed size.

## Open verifications (each becomes a spike or a pinned fact)

1. go-imap v2 beta API stability for QRESYNC and IDLE, and that `imapmemserver` supports enough
   (CONDSTORE/QRESYNC/MOVE) to test sync; if not, extend it in `mailworld`.
2. `modernc.org/sqlite` FTS5 availability, `go build` time and peak RAM on the potato.
3. Safari zstd `Content-Encoding` support on the operator's iOS/iPadOS versions (brotli is the
   Safari default; see `PERFORMANCE.md`).
4. Pure-Go PDF text extraction quality.
5. HEIC need on real iOS.
6. Whether `tailscale serve` HTTPS is worth enabling (service workers/PWA need a secure context).
