# ADR 0006 — CSS and text scope

Status: accepted

## CSS

- Units: logical `px`, `%`, `em`, `rem`, unitless zero and `auto`. A NeferGUI `px` is a logical pixel; the renderer converts to physical pixels with the Wayland scale.
- Not supported: viewport and physical units, `calc`, `min`, `max`, `clamp`.
- Selectors: type, class, ID, descendant, direct child; pseudo-classes `hover`, `active`, `focus`, `focus-visible`, `disabled`.
- A property table is frozen before parser work (`docs/css.md`). Unknown declarations are ignored with a diagnostic. A malformed rule never corrupts its neighbours.
- `Row` and `Column` ship overridable flex defaults. `Stack` paints its first child at the back, sizes to the largest child and hit-tests in reverse paint order. Semantic helpers set a CSS type and an accessibility role.

## Text and images

- `go-text/typesetting` handles font parsing, segmentation, bidi, fallback and shaping.
- `golang.org/x/image/font/sfnt` with a Go rasterizer produces grayscale masks.
- Glyph positions stay fractional. Atlas keys include face, glyph, physical size, variation coordinates and subpixel phase.
- Mono-channel bounded atlases, premultiplied alpha, linear-light blending, sRGB output. No LCD or SDF/MSDF text.
- Font fixtures are pinned with their licenses. Golden images need explicit human approval and are backed by independent metric and coverage checks.
- Images: Go `image.Image`, PNG and JPEG from the standard library. No SVG or animation.
