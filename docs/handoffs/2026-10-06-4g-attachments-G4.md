# 4g — outgoing attachments and images (the G4 dependency gate)

Stage 4g is "Claude leads decoders and dependencies, DeepSeek the UI" (`next_steps.md`, round 60), so
the dependency choice is settled **before any code**, in the spirit of `docs/CHUNK4-BRIEF.md` section
5. This is the gate-G4 checkpoint: what 4g needs, what stdlib covers, the `STACK.md` entry, and the
phone's real HEIC behaviour.

**Nothing here is implemented yet.** The four operator choices are recorded in `docs/qa-log.md` round
65.

## 1. What 4g must do

1. Attach files and photos to an outgoing message and to a saved draft; the Sent copy keeps them.
2. Prepare photos: apply EXIF rotation, strip EXIF/location by default, optionally downscale
   (Original / Large / Medium / Small, default Large).
3. Inline images (`cid:`) placed in the body.
4. "From your mail": attach an attachment already in the mirror with no browser upload.
5. Bound everything (size, count, type, upload time) with a defined outcome.

## 2. Gate G4 — the decision (operator, round 65)

**Browser-first, no server-side image decoder, no new dependency.**

- **Where images are prepared:** in the browser, extending `web/src/lib/photo.ts`. The file input's
  blob goes through `createImageBitmap(file, { imageOrientation: 'from-image' })` (which applies EXIF
  rotation and, in Safari, decodes HEIC), then a canvas draw at the chosen edge, then
  `canvas.toBlob('image/jpeg' | 'image/png', 0.85)`. The re-encode **strips all EXIF/GPS metadata by
  construction**. This is the same path account photos already use.
- **The phone hands back JPEG.** The operator reports that iOS Safari sends a JPEG (not HEIC) through
  a web photo picker, so no HEIC decoder is ever needed; the browser decode still covers a HEIC that
  arrives another way (Safari decodes it natively). A browser that cannot decode the image shows the
  existing friendly `PhotoError`.
- **The server never decodes an image.** It sniffs a magic number, bounds the size, streams the bytes
  to disk, and builds the MIME. Nothing in the Go tree changes its dependency set, so the G4/T7 stop
  does not trigger.
- **What stdlib covers (for the record):** `image/jpeg`, `image/png`, `image/gif` and
  `golang.org/x/image/webp` could decode, `golang.org/x/image/draw` could scale, but stdlib cannot
  read EXIF orientation and nothing pure-Go decodes HEIC without a WASM decoder. The browser does all
  three for free, so the server path stays a byte pipe.
- **`STACK.md` entry:** the "Images" row is narrowed to say the browser does the preparing (no
  server decoder, no new module); the `golang.org/x/image` proposal is removed from the open
  questions.
- **Fuzz/hostile-input corpus:** since no Go decoder is added, the corpus is (a) Vitest cases feeding
  truncated/hostile blobs to `prepareImage` (a real `createImageBitmap`/canvas fake), and (b) Go tests
  proving arbitrary bytes still flow safely through the attachment store and builder (a filename with
  CR/LF/NUL, an executable type, a 25 MiB blob).

## 3. Settled decisions (operator, round 65)

1. **Browser prepares images** (section 2).
2. **Limits:** 25 MiB per file, 25 MiB total raw attachments, 20 attachments. Base64 inflation
   (~33%) keeps the built message well under Purelymail's `EHLO SIZE` (~48.8 MiB) and matches the
   existing `SendFailed` canvas copy.
3. **Inline images are in 4g**, not deferred to 4h. An image marked inline becomes a
   `multipart/related` part with `Content-ID: <uploadId@ivy>`; the compose screen inserts
   `![name](cid:<uploadId@ivy>)` so the markdown renderer emits the matching `<img>`. The outgoing
   HTML policy must allow the `cid:` scheme on images.
4. Related choices settled here rather than asked: EXIF strip is honoured by the browser re-encode;
   "Original" size still re-encodes at native resolution so location is removed unless the operator
   turns "Remove location" off, in which case the untouched bytes are uploaded.

## 4. Upload staging (transient, `data/uploads/`)

Bytes are content-addressed under `data/uploads/<sha256>` (reuse `internal/blobstore.Open`), so the
same photo attached twice is stored once. `data/uploads` is deliberately **not** the disabled-mail
blob store: that one is backup-mirrored and reconciled against disabled rows, and outgoing staging is
neither. Metadata lives in a new `state.db` table.

### `state.db` migration 15 — `uploads`

| column | meaning |
|---|---|
| `id` | staging id (injected); what a compose request references |
| `account_id` | whose mailbox (scopes every read and delete) |
| `hash` | `sha256` hex of the bytes; the file name under `data/uploads/` |
| `name` | the original file name (bounded, header-safe) |
| `mime` | the declared/sniffed content type (bounded) |
| `size` | byte length |
| `created_at` | time, for the orphan sweep |

One row per selection; several rows may share a `hash`. A file is removed only when no `uploads` row
for that hash remains.

**Lifetime.** Uploads are staged from pick to commit and are freed when the owning draft is
`sent`/`discarded` or the send reaches a terminal state, plus an age sweep of 7 days for anything
abandoned. Resume and undo never depend on the original staging files: they re-materialise fresh
uploads from the stored MIME (section 7), so a staging file that was swept is invisible to the
operator. `DELETE /accounts/{id}/uploads/{uploadId}` frees one immediately when the screen removes
it.

## 5. HTTP surface

| Method + path | Purpose |
|---|---|
| `POST /accounts/{id}/uploads?name=` | Raw body streamed to disk (never `io.ReadAll`); `Content-Type` declares the type. Returns `{id, name, mime, size}`. |
| `GET /accounts/{id}/uploads/{uploadId}` | Streams staged bytes (thumbnails, resume). |
| `DELETE /accounts/{id}/uploads/{uploadId}` | Frees a staged upload. |
| `GET /accounts/{id}/mail-attachments?q=&limit=` | Recent mirrored attachments for "From your mail". |
| `POST /accounts/{id}/uploads/from-mail` | `{messageId, path}` → server streams the mirror part into staging; no browser upload. |

`SendRequest` and the draft save gain:

```yaml
attachments:
  - id: string      # a staged upload id
    inline: boolean # default false
```

The server resolves each id (account-scoped), uses the **stored** name and mime (a request never
supplies a filename), and enforces the total/count limits. A missing or foreign id is `400
invalid_message` / `404`. The response carries each attachment's `name`, `size` and `inline` so the
UI can render without a second round trip.

## 6. Builder (`compose/`)

`Message` gains `Attachments []Attachment`:

```go
type Attachment struct {
    Filename string
    MIMEType string
    Content  []byte // bounded by MaxTotalAttachmentsBytes before it is read
    Inline   bool
    CID      string // without <>, required when Inline
}
```

- `validate` checks: count ≤ `MaxAttachments`; total content ≤ `MaxTotalAttachmentsBytes`; each
  filename non-empty, no CR/LF/NUL, ≤ `MaxFilenameBytes`; each mime no CR/LF/NUL, ≤ `MaxMIMEBytes`;
  an inline part has a syntactically valid CID. Bad values are rejected, never stripped.
- `Build` adds non-inline parts with `AddAttachment` and inline parts with `AddInline`; enmime emits
  `multipart/related` (inlines) nested in `multipart/mixed` (file attachments) around the
  `multipart/alternative`. A golden test pins the structure.
- `Bcc` handling is unchanged: the wire copy has no `Bcc` header, the Sent copy keeps it, and both
  keep the attachments.
- The outgoing HTML policy allows the `cid:` scheme so an inline `<img>` survives sanitising.

## 7. Drafts, send and resume

- **Draft save** builds the MIME with its attachments and stores the immutable `drafts.body` (4d,
  unchanged); `compose_json` keeps the attachment ids so the live session can save again. A draft
  with attachments is therefore self-contained in `state.db` (≤ ~34 MiB per live version; terminal
  rows are pruned as in 4d).
- **Send** builds the wire and Sent copies with attachments, stores both in `send_queue`, and frees
  the staged uploads once the row is committed.
- **Resume** a local draft: the server returns `compose_json` plus, for each attachment in the stored
  body, a re-materialised staging upload id, so the screen has fresh ids and can re-save.
- **Undo** re-materialises the same way from the send row's stored Sent body, so attachments come
  back with the draft.
- **Resume a server-only draft** (made in Apple Mail): the existing raw parse is extended to surface
  its attachment parts as staged uploads.

## 8. Failure paths and bounds (STANDARDS 4a additions)

| Limit | Value | Above it |
|---|---|---|
| One uploaded file (`compose.MaxAttachmentBytes`) | 25 MiB | `413 too_large`, nothing staged |
| Total attachments on one message (`compose.MaxTotalAttachmentsBytes`) | 25 MiB | `400 invalid_message` before the build |
| Attachments per message (`compose.MaxAttachments`) | 20 | `400 invalid_message` |
| One filename (`compose.MaxFilenameBytes`) | 255 bytes | `400 invalid_message` |
| One attachment mime (`compose.MaxMIMEBytes`) | 255 bytes | `400 invalid_message` |
| Dangerous attachment types | executables, scripts, HTML, SVG (explicit deny list) | `400 bad_type`, name repeated back |
| Staged uploads older than `store.MaxUploadAge` | 7 days | swept; resume re-materialises from the stored body |
| "From your mail" list | 50, default 20 | clamped |
| Upload body read | streamed to disk, never whole in memory | a stalled upload is cut by the request deadline |

## 9. Frontend

- `AttachSheet` gets three real `<input type="file">` controls (Photos `accept="image/*" multiple`,
  Camera `accept="image/*" capture="environment"`, Files multiple) and a real "From your mail" list.
  The Photo size row and location note read the real settings.
- `photo.ts` gains `prepareImage(file, {size, stripLocation})`, tested against a fake
  `createImageBitmap`/canvas, including truncated and hostile blobs.
- Compose keeps attachment state `{id, name, size, mime, inline, previewUrl}`; picks upload with a
  visible progress/spinner; remove calls `DELETE`; "Insert image" uploads and inserts the `cid:`
  markdown at the cursor; send and every autosave include the attachment list.
- Send is no longer blocked by attachments; the over-limit path shows the designed Not-sent sheet
  ("Remove X and send").
- Playwright (phone + desktop): attach a file and send; the too-large sheet; insert an inline image.

## 10. Tests (test-first)

- `compose/attach_test.go`: structure golden; inline cid; Bcc only on the Sent copy; hostile
  filenames/mimes; count/total/size limits; a fuzz target for attachment bytes and names.
- `gateway/uploads_test.go`: streamed upload, 413, sniff/deny, dedup, delete, account scoping,
  from-mail copy, sweep.
- `gateway/send_test.go` / `gateway/drafts_test.go`: attachments round-trip; over-limit refusal;
  resume/undo re-materialisation; staged uploads freed.
- `store/uploads_test.go`, and `internal/blobstore` coverage for the reused store.
- `web/src/lib/photo.test.ts`: downscale, rotate, strip, alpha→PNG, hostile input.
- Playwright: attach, send, too-large, inline.

## 11. Left for the operator

- Live send with a photo and a PDF to an address the operator owns; check the recipient and the Sent
  copy. Confirm once more that the phone picker hands back a JPEG.
- The single-account "From your mail" list against the real mailbox.
