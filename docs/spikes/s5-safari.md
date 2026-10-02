# S5: how does Safari behave?

Run 2026-10-02 with `spikes/s5-safari/` (removed from the tree; recover it with
`git show a049bfb:spikes/s5-safari/`; a throwaway test page and server, reached over Tailscale,
storing metadata only). **Tested so far: one iPad, Safari 26.5, over HTTPS (`tailscale serve`).**
Not yet tested: an iPhone, plain HTTP over the tailnet IP, and the `image/heic` upload box. Treat
everything below as iPad-only until the iPhone runs.

## Facts

| Question | Result |
|---|---|
| `Accept-Encoding` sent | `gzip, deflate, br, zstd`; **zstd is advertised** |
| Encodings decoded | identity, gzip, br and **zstd** all decoded correctly via `fetch` (the same 128 KB JSON was 8.3 KB gzip, 3.6 KB br, 3.59 KB zstd) |
| `tailscale serve` proxy | passes `Accept-Encoding` and `Content-Encoding: br` through unchanged, so Ivy's own negotiation works behind it |
| User agent | iPadOS reports a **desktop `Macintosh` UA**; do not detect iPad by UA. Use `(pointer: coarse)` and `(hover: none)` (both matched) |
| Viewport | portrait screen 834 x 1210, inner 1210 x 702 in the tested orientation, DPR 2 |
| Safe area | bottom inset 20 px (home indicator); top, left and right 0 in this orientation. Needs `viewport-fit=cover` |
| Viewport units | `100vh` = 777 px but `100dvh` = `100svh` = 702 px (`100lvh` = 777). **Use `dvh`**, never `vh`, for full-height shells |
| CSS and API support | `dvh`, container queries, `:has()`, `color-mix()`, view transitions and `DecompressionStream` are all supported |
| Dark mode | `prefers-color-scheme: dark` matched |

### Photo uploads (iPad)

A camera-shot photo (HEIC at source, per the operator) was uploaded twice, through
`accept="image/jpeg,image/png"` and through an input with **no `accept` attribute**: both arrived
as a real **JPEG** (verified by magic bytes `ffd8`, 5712 x 4284, 3,659,001 bytes, identical in
both uploads), declared `image/jpeg`, extension `.jpeg`. An earlier upload from another app
(through `accept="image/*"`, 3023 x 3577, 3.5 MB) was also JPEG, with an EXIF block. So **the
picker converts HEIC to JPEG** even without a filter. (The original being HEIC rests on the
operator's word; the server only sees the converted bytes.)
- **Decision:** no HEIC decoder is needed for the compose/upload path. Server-side, still sniff
  the magic bytes and treat a `ftyp` brand of `heic`/`mif1` as a clear error, never trust the
  declared type.
- Uploaded photos are large (3 to 4 MB, up to about 24 megapixels): the planned downscale and
  EXIF strip are required, and EXIF can carry location, so stripping is a privacy requirement.
- **Still open:** the `image/heic` box, the iPhone, and HEIC arriving as a *received* attachment
  (Safari displays HEIC natively, but a server-side thumbnail would need a decoder).

### Rendering hostile mail in an iframe

Six iframe configurations loaded the same hostile page (script, remote image, meta refresh).
`script`, `remote img` and `refresh` mean the thing happened (bad):

| Configuration | Script ran | Remote image fetched | Meta refresh navigated |
|---|---|---|---|
| A `srcdoc`, `sandbox=""` | no | **yes** | no |
| B `srcdoc`, `sandbox="allow-scripts"` | **yes** | **yes** | **yes** |
| C `src` + CSP header, `sandbox=""` | no | no | no |
| D `src` + CSP header, `allow-scripts` | no | no | **yes** |
| E `srcdoc` + meta CSP, `allow-scripts` | no | no | **yes** |
| F `src`, no CSP, `allow-scripts` (baseline) | **yes** | **yes** | **yes** |

- **A sandbox alone does not block remote content** (A fetched the tracking image). Only the CSP
  stops it.
- **Only C blocks everything.** CSP stops scripts and images even when `allow-scripts` is granted
  (D, E), but a meta refresh still navigated, so `allow-scripts` must never be granted.
- **Decision:** render mail bodies in `<iframe sandbox="" src=...>` served by Ivy with a strict
  `Content-Security-Policy` header (`default-src 'none'; style-src 'unsafe-inline'`, with `img-src`
  limited to the proxy/attachment origin). A `srcdoc` iframe cannot carry a header, so it is not
  acceptable without a meta CSP, and a meta CSP alone did not stop navigation.
- **Open consequence, untested:** with no scripts inside, the iframe height cannot be set from
  within. Hypothesis: `sandbox="allow-same-origin"` (still no `allow-scripts`) would let the
  parent measure `contentDocument` safely. Test before relying on it.

## Not tested

iPhone; plain HTTP vs HTTPS side by side (the HTTP URL was served but not run); the `image/heic`
upload box; `capture` camera input; Firefox; whether `tailscale serve` HTTPS needs any per-device
setup beyond tailnet certs (it worked here without any).
