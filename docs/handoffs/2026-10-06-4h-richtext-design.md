# 4h — the rich-text editor design

Stage 4h is the last, deferrable chunk 4 stage (`next_steps.md`, round 60). It has no formal gate,
but the choices below decide the whole stage, so five were put to the operator before any code and
recorded in `docs/qa-log.md` round 66. **Nothing here is implemented yet.**

## 1. What 4h must do

1. Turn the inert Bold / Italic / Link / List buttons in `web/src/routes/compose/+page.svelte` into a
   working rich-text editor, on the operator's main device (iOS Safari).
2. Keep the existing Markdown textarea as a mode; rich text is the default.
3. Ensure the editor's output goes through **the same builder and sanitiser** as markdown: the
   outgoing HTML is narrowed by `compose`'s `outgoingPolicy`, and a `text/plain` alternative is
   derived for recipients whose client cannot show HTML.
4. Round-trip the format through drafts: a rich draft resumes as rich text, a markdown draft as
   markdown, and a draft made in another client (Apple Mail) keeps its formatting.
5. Do not add a client sanitiser library and do not break the critical-path budget.

## 2. The decisions (operator, round 66)

1. **Editor: `squire-rte` v2.4.9**, MIT, zero dependencies, **16.1 KiB brotli** for the whole
   editor. Measured against Quill core (38.9), Lexical (57) and a minimal TipTap (98.3). Squire was
   built for Fastmail's mail compose, handles arbitrary pasted/quoted HTML, uses its own formatting
   engine instead of `execCommand`, ships TypeScript types, and is actively maintained. It loads only
   in the `/compose` route chunk, so the critical-path budget is untouched (`docs/STACK.md`).
2. **Both editors, rich default.** A small mode pill toggles Markdown ↔ rich text.
3. **The mode is fixed once you type.** The pill is active only while the body is empty; a saved
   draft reopens in its saved format. No HTML↔Markdown converters, so nothing is ever rewritten.
4. **`bodyFormat` on the wire, sanitised server-side.** `SendRequest`, `DraftRequest` and
   `DraftResume` gain `bodyFormat: "markdown" | "html" || absent`. Absent keeps the current meaning
   (`markdown: true` = markdown, else plain), so stored draft JSON and older clients keep working.
5. **Our own allow-list `sanitizeToDOMFragment`, no DOMPurify** (STACK: no client sanitiser library).

## 3. The body contract

`compose.Message` currently carries `Text` (the operator's text) plus `Markdown bool`. Replace the
bool with an explicit format:

```go
type BodyFormat int
const (
    BodyPlain    BodyFormat = iota // text/plain only
    BodyMarkdown                   // goldmark → text/html, as today
    BodyHTML                       // the operator's HTML, sanitised
)
```

Then `Build`:

| Format | `text/plain` | `text/html` |
|---|---|---|
| `BodyPlain` | `wireText(Text, true)` | — |
| `BodyMarkdown` | `wireText(Text, true)` | `renderMarkdown(Text)` (unchanged) |
| `BodyHTML` | `wireText(htmlToText(Text), true)` | `sanitizeOutgoingHTML(Text)` |

- `sanitizeOutgoingHTML` runs the operator's HTML through the existing `outgoingPolicy` (the same
  one goldmark's output already passes through), never the reader's inbound policy.
- `htmlToText` is a small local converter over `golang.org/x/net/html` (already a dependency via
  bluemonday): block elements become line breaks, `<br>` a newline, list items keep a marker,
  entities decode, `<script>/<style>` content is dropped. It is deliberately not `render`'s
  `plainText` (compose stays a pure builder, not coupled to the reading surface).
- Every header-bound value is still validated as before; the HTML is body-only.
- `MaxBodyBytes` still bounds the raw operator content in either format.

**Security.** The author of the HTML is the operator, but it can contain pasted third-party markup,
so it is hostile enough to narrow. The server policy is authoritative; the client walker in section
4 protects only the live editing DOM. Both are tested against a corpus (CR/LF/NUL is already covered
by the builder; here: `<script>`, `onerror=`, `javascript:` hrefs, `<iframe>`, `<style>`, `data:`
URLs, malformed/nested tags).

## 4. The client

- **`RichEditor.svelte`** wraps Squire: creates it on mount with `{ blockTag: 'P',
  sanitizeToDOMFragment }`, reports `getHTML()` on the `input` event (debounced through the existing
  autosaver), sets initial HTML with `setHTML`, and calls `destroy()` on unmount. Button state comes
  from `hasFormat()` on the `pathChange` event.
- **`sanitize.ts`** is the allow-list walker: parse with `DOMParser`, keep the tags/attributes the
  outgoing policy allows (`p br hr strong em b i u s del mark ul ol li blockquote pre code h1-h6 a
  img`; `href/title` on `a`, `src/alt/title` on `img`), allow only `http`, `https`, `mailto`, `cid`
  URLs, unwrap everything else, drop comments and `<script>/<style>` wholesale. It returns a
  `DocumentFragment` in the editor root's document. Vitest corpus beside it.
- **The mode pill** sits in the format bar. While the body is empty it switches freely; once it has
  content it is a static label. Switching swaps the textarea for `RichEditor` (or back) and keeps the
  existing body string only when empty.
- **The format bar** enables the four buttons in rich mode (`squire.bold()`, `.italic()`,
  `.makeLink(url)` behind a small inline URL prompt, `.makeUnorderedList()` / `.makeOrderedList()`)
  and disables them in Markdown mode. Their pressed state follows `pathChange`.
- **Inline images (4g)** keep working: the insert-image button drops `![name](cid:…)` in markdown
  mode and, in rich mode, inserts an `<img src="cid:…">` via `squire.insertHTML`.

## 5. Drafts and the API

- `api/openapi.yaml` gains `bodyFormat` on `SendRequest`, `DraftRequest` and `DraftResume`; the Go
  and TS generated types are regenerated and `make drift` stays green.
- `gateway/send.go` and `gateway/drafts.go` map it into `compose.BodyFormat` (absent ⇒ derived from
  `markdown`, which stays accepted for compatibility).
- `draftResume` returns the stored `bodyFormat` (it already returns `text` + `markdown` verbatim).
- `draftResumeServer` (a draft with no local compose row, e.g. from Apple Mail) uses `mime.Parse`:
  if the parsed `HTML` is non-empty, resume `bodyFormat: html` with that HTML, else `markdown` with
  the text. This preserves a rich draft's formatting.
- No migration: the draft's `compose_json` already stores the whole request, so `bodyFormat`
  round-trips with it.

## 6. Bounds and failure paths

- Body size: `MaxBodyBytes` (1 MiB) in either format, refused before anything is built.
- HTML line length: `wireText(..., false)` already leaves an over-long HTML line intact rather than
  corrupt a tag; a body whose text conversion is empty is allowed (an HTML-only draft is normal).
- A paste Squire cannot sanitise inserts nothing and is reported; a load of hostile HTML is narrowed,
  never trusted.
- The editor is opt-in to the compose route; a browser without `contenteditable` is out of scope
  (Squire supports all reasonably recent browsers, iOS/desktop Safari included).

## 7. Tests (definition of done)

- **Go (`compose`):** the `BodyHTML` path (plain-text derivation, sanitisation, cid images),
  hostile-HTML corpus, empty body, the `BodyPlain`/`BodyMarkdown` paths unchanged; then
  `gateway` send and draft round-trips with `bodyFormat`.
- **Vitest:** `sanitize.ts` corpus, the `bodyFormat` mapping, the mode-pill "fixed once you type"
  behaviour.
- **Playwright (WebKit phone + Chromium desktop):** compose in rich mode, bold/italic/list/link,
  switch to Markdown on an empty body, send and see the HTML survive the draft round-trip, plus an
  `unconfirmed`/failure path unchanged.
- `make check`, `go test -race ./...`, `pnpm test`, `pnpm exec playwright test`, and the measured
  `/compose` route chunk recorded in `PERFORMANCE.md`.

## 8. Not in scope

HTML→Markdown or Markdown→HTML conversion, headings beyond what the four buttons need, tables, code
hosting, collaborative editing, and a client sanitizer library.
