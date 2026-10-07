# 5h. Vision

## Purpose

Let a vision model read images (attachments and inline `cid:` images) on opted-in accounts, so the
operator can ask "what is this" and receipts or screenshots become searchable text. **Soon, not
first**; the riskiest stage of the chunk because it decodes hostile images and sends them off the
device.

## Depends on

5a (the `See` request type, the vision switch), 5d (withheld mail and `attachment_worth_reading`).
Receipts (5g) and Ask Ivy (5i) can consume its output.

## Where we start

- Attachments are mirrored and extracted at tiers 0 and 1; images have no text extraction.
- **There is no server-side image decoding at all** (checked 2026-10-07: no `image.Decode` in the Go
  code). The EXIF stripping and downscale for outgoing photos live in the browser
  (`web/src/lib/photo.ts`). Inbound images are served to the reader as-is behind the sandbox. So this
  stage is the first place the server decodes a hostile image, which is why it has a design gate
  before any dependency is chosen.
- `internal/mailworld` fakes the vision endpoint (`handleVision`), so tests need no provider.
- Safari image behaviour is a partial spike (S5): iPad over HTTPS only, no iPhone, no HEIC check.

## Settled

- **Vision is per-account opt-in and has its own switch** (`vision_enabled`); an LLM-off account is
  never sent an image (`ARCHITECTURE.md` 7).
- **Attachments and inline `cid:` images only.**
- **Gates:** automatic only for mail Jev flags (`attachment_worth_reading`, judged from filename,
  surrounding text and type, since Jev sees no pixels) plus an on-demand **read this image** action;
  size and dimension filters, content-hash dedupe, and a monthly cap (`ARCHITECTURE.md` 7, PLAN.md 3).
- **Model output is untrusted plain text** (invariant 5), never HTML, never labelled as AI.
- **OCR (Tesseract) is far-out,** only if the operator finds it useful.
- **Withheld mail (tripwire, sensitive) is never sent to vision** (brief invariant 8).

## Scope

1. **5h.1 Design gate (G4).** What is needed (decode, downscale, re-encode), what stdlib covers, the
   `STACK.md` entry for any dependency, the limits, and exactly what leaves the device. Nothing is
   chosen before the operator sees this.
2. **5h.2 Prepare.** A bounded, pure-Go image pipeline: decode with dimension and byte limits,
   downscale, strip metadata, re-encode, hash for dedupe. Decompression-bomb safe.
3. **5h.3 `See` through the gate** with the withheld and opt-in checks, the dedupe lookup, the cap,
   and a ledger row per image.
4. **5h.4 Storage and search.** The text result stored keyed by attachment hash (derived), indexed by
   the search pipeline, and shown on the attachment tile.
5. **5h.5 On-demand action** "read this image" in the reader, and the flagged-mail automatic path.

## Tests and exit

- Hostile images: decompression bombs, truncated files, wrong magic, huge dimensions, animated and
  multi-frame, polyglots; all bounded and refused with a recorded outcome, never a crash or a hang.
- A withheld or LLM-off account's image never appears in any gate request body.
- Dedupe: the same image in two messages, or moved, is read once.
- The cap: refused at zero cost, recorded, and the on-demand action says why.
- **Exit:** reading an image works on the dev stack with the fake and, for the operator, live on a
  real receipt photo; HEIC behaviour on the phone is their own check.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| Image bytes | a stated maximum | refused, `rejected`, shown |
| Dimensions and pixel count | stated maxima, checked from the header before decoding | refused before allocation |
| Frames / metadata | first frame only, metadata stripped | ignored |
| Decode time | a deadline per image | cancelled, recorded |
| Images per message / per day | stated | the rest waits or needs the on-demand click |

## Decisions (Q&A with the operator, 2026-10-07)

1. **HEIC is out of scope, optional later.** The operator tested it: a photo uploaded from an iPhone
   through Safari arrives as JPEG, so there is nothing to decode. This replaces the unverified HEIC
   note from spike S5. In scope: JPEG, PNG, WebP and GIF; the dependency question at gate G4 now only
   covers those.
2. **The result is shown on the attachment tile, indexed for search and offered to receipt extraction**
   (5g) for photographed receipts. Stored keyed by image hash.
3. **Automatic reading: flagged mail only, bounded by the monthly cap alone** (no daily ceiling).
4. **The on-demand button shows an estimate** ("Read this image (about $0.002)") from registry pricing,
   a single click.
5. **What is sent: the downscaled, metadata-stripped image plus the surrounding message text.** That
   text is the same clipped, stripped state every model call uses (5b) and passes the same withheld,
   opt-in and one-account rules; sensitive or withheld mail is never sent.
6. **Decorative and tracking images are skipped** by size and repetition (assumed retained; the
   operator's answer covered what is sent, so confirm at build).
7. **Model: the registry's default vision-capable model** (overridable per feature).

## The questions as asked

1. **Dependencies:** is stdlib `image` plus `x/image` enough for JPEG/PNG/WebP/GIF, and is HEIC in
   scope (S5 is unverified on iPhone)? This is gate G4.
2. **Model and prompt:** which vision model id, and the prompt for each use (describe, extract text,
   read a receipt)?
3. **What leaves the device:** the downscaled image only; maximum edge and quality; is any text
   context sent with it?
4. **Result use:** shown only on demand, or also indexed for search and fed to receipt extraction?
5. **Automatic reading:** `attachment_worth_reading` threshold, a per-day image ceiling, and whether
   the first enable offers a backfill (almost certainly not).
6. **Inline `cid:` images:** tracking pixels and decorative images should never be read; what filter
   (size, dimensions, repetition) decides?
7. **Cost display:** is a per-image cost estimate shown before the on-demand click?
