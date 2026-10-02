# Ivy app icon

A moonlight-lilac ivy leaf with drifting fireflies on the night-garden gradient, generated with an
image model (2026-10-02, qa-log round 28) from the prompt in the project history.

| File | What |
|---|---|
| `ivy-icon.png` | 1024px master with a transparent squircle (corners cut out, edge anti-aliased) |
| `ivy-icon-full-bleed.png` | 1024px opaque square: the dark gradient extended into the corners. iOS applies its own rounded mask and renders transparency as black, so the home-screen icon is made from this one |

`web/static/` holds the derived sizes: `favicon.ico` (16/32/48), `favicon-32.png`,
`apple-touch-icon.png` (180, from the full-bleed master), `icon-192/512.png` (manifest) and
`icon-256.png` (the logo in the desktop rail and welcome screen).

Regenerating the sizes from a new source: flood-fill the white corners from the image border, grow
that region by ~2px to remove the JPEG fringe, blur the alpha by ~1px; for the full-bleed version,
erode the valid area by ~12px (and clear a 14px frame, because the squircle touches the border
mid-edge) and push the edge colours outward. Then Lanczos-resize. Check the cutout composited on a
loud colour, and check 16px and 32px on both a dark and a light tab strip.
