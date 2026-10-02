# S6: is pure-Go PDF text extraction good enough?

Run 2026-10-02. Code: `spikes/s6-pdf/` (throwaway; removed from the tree, recover it with
`git show a049bfb:spikes/s6-pdf/`). Candidates, all cgo-free: `ledongthuc/pdf`,
`dslipak/pdf` (a fork) and `go-pdfium` in WebAssembly mode (PDFium compiled to wasm and run by the
pure-Go `wazero`). Reference: `pdftotext` (poppler), used only as an oracle. Fixtures: a generated
receipt, a generated invoice with a table and right-aligned totals, an image-only "scan" with no
text layer, three damaged files, and one **real 212-page, 14.4 MB PDF** (the operator's, processed
locally and on the board and deleted from the board after; its content was not read or committed).

## Results

| | `ledongthuc` | `dslipak` | `pdfium` (wasm) |
|---|---|---|---|
| Receipt and invoice text | correct, word spacing and reading order good | **words glued together** (`SupplyReceipt for order 4821Date:`) | correct |
| Image-only scan | 0 words (detectable) | 0 words | 0 words |
| Real 212-page PDF, recall of `pdftotext`'s distinct words | 96.6% | 97.3% | **99.5%** |
| Time, 212 pages, dev laptop | 0.43 s | 3.9 s | 2.9 s (+1.6 s init) |
| Peak RSS, dev laptop | 16 MiB | 15 MiB | **336 MiB** |
| **Time on the board, 212 pages** | **5.7 s** | not run | **62.7 s** |
| **Peak RSS on the board** | **15 MiB** | not run | **337 MiB** (296 MiB just to start) |
| Start-up on the board | none | none | **about 31 s** to init the wasm runtime before reading a one-page file |
| Truncated or garbage file | clean error | clean error | clean error |
| One byte range corrupted | **errored on page 1, no text** | recovered 16 words | recovered 22 words |

## Decision

1. **Default: `ledongthuc`.** It is fast (5.7 s for 212 pages on the board), uses about 15 MiB,
   keeps word spacing, and recalls about 97% of the words a reference extractor finds, which is
   plenty for search and receipt fields. Wrap every call in `recover` and a timeout.
2. **No text layer is detectable** (0 words on a scan): route those to the vision path under its
   own budget, never silently skip.
3. **`pdfium` is a rare fallback only, not a default.** It is the most accurate and the most
   forgiving of damaged files, but on the board it costs about 31 s of start-up and about 300 MiB,
   which is too much to pay per attachment next to the live services. If used: one lazily created
   long-lived instance, a semaphore of one, a size cap, and skipped when free memory is low.
   Use it when `ledongthuc` errors or returns suspiciously little text for the page count.
4. **Drop `dslipak`:** its word-gluing makes it unusable as a primary or backup.
5. **Bound the work:** cap pages and bytes extracted per file (the real file was 14 MB), extract
   in the background after sync, and store the text so it is done once.

## Not tested

Password-protected PDFs, PDFs with embedded fonts using custom encodings or right-to-left text,
and OOXML (docx/xlsx) extraction with the standard library `archive/zip` plus `encoding/xml`
(expected to be straightforward, not run). The synthetic fixtures use plain Latin text, so the 97%
recall comes from one real document only.
