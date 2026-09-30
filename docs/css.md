# NeferGUI CSS contract

This is a deliberately bounded CSS dialect, not browser CSS. Identifiers and property names are ASCII case-insensitive except custom properties (case-sensitive), IDs, classes and quoted font names. Whitespace and `/* ... */` comments separate tokens. Unknown properties and invalid static values are ignored with line/column diagnostics in `Sheet.Diagnostics` (or the diagnostics returned by `ParseInline`). Declarations remain in parsed sheets for inspection, but are omitted from compiled styles. Values containing `var()` are validated only at computed-value time, when substitutions are available; invalid computed values fall back as described below and do not emit compile-time diagnostics. There is no selector nesting, at-rule, animation or media query support.

## Grammar and cascade

Stylesheet = repeated `selector-list { declaration* }`; declaration = `property : value [!important] ;` (last semicolon optional). A bad declaration is skipped to `;`/`}`; a bad rule is skipped through its balanced block. Selectors: type (`app`, `button`, etc.), `.class`, `#id`, compounds, whitespace descendant, `>` child, comma lists, `:hover`, `:active`, `:focus`, `:focus-visible`, `:disabled`, `:checked` (a checked checkbox or selected radio). No universal, attribute, sibling, negation, pseudo-element or functional selector. Specificity is lexicographic (IDs, classes+pseudo-classes, types); lists use the highest *matching* selector. A later declaration wins a tie. NeferGUI UA defaults have lower origin than author declarations; **author `!important` wins over all normal declarations**, then origin, specificity and source order. UA defaults do not use important. Duplicate declarations retain source order. Unset inherited properties inherit; other properties use initial. Explicit `inherit`, `initial`, `unset` are supported. Custom properties inherit by default and are resolved after cascade; `var(--name[, fallback])` can appear anywhere in a value, nested and repeated; missing/cyclic variables without fallback invalidate the consuming declaration at computed-value time (inherit for inherited properties, initial otherwise), **not** the next candidate in the cascade. Fallback can be another var. Custom property values are token sequences, not parsed until use; an empty value is valid.

Units: logical `px`, `%`, `em`, `rem`, unitless `0`, `auto` where explicitly allowed. `em` resolves with this element's computed font size (font-size itself uses parent's size), `rem` with root font size; `%` stays unresolved for layout. Negative lengths only where noted. Colors: CSS named colors (full CSS named set), `transparent`, `currentColor`, `#rgb`, `#rgba`, `#rrggbb`, `#rrggbbaa`, `rgb()`/`rgba()` with comma or space channel syntax and optional `/` alpha, `hsl()`/`hsla()` similarly. RGB channels 0–255 or %, alpha 0–1 or %, hue degrees (unitless or deg), saturation/lightness %. Clamp color channels, reject non-finite numbers. Computed colors are straight sRGB RGBA float64 [0,1]; renderer converts to premultiplied linear. `currentColor` refers to computed `color`; on `color` itself it means inherited color. Lengths and opacity reject non-finite/overflow numeric input.

## Property table

Notation: `L` = nonnegative length (`px|%|em|rem|0`), `S` = signed length, `C` = color, `A` = `auto`, `N` = finite nonnegative number, `W` = border width `L`, `B` = border style `none|solid`. Four-value shorthands use CSS top/right/bottom/left order (1 = all, 2 = vertical/horizontal, 3 = top/horizontal/bottom, 4 = each). Every row states initial / inheritance / applicable elements; `all` means every NeferGUI element type.

| Property | Value grammar | Initial | Inherited | Applies to |
|---|---|---|---|---|
| color | C | black | yes | all |
| opacity | number 0..1 | 1 | no | all |
| background-color | C | transparent | no | all |
| background | C (resets background-color only) | transparent | no | all |
| border-{top,right,bottom,left}-width | W | 0 | no | all |
| border-{top,right,bottom,left}-style | B | none | no | all |
| border-{top,right,bottom,left}-color | C | currentColor | no | all |
| border-width / border-style / border-color | 1–4 W / B / C | per-side initials | no | all |
| border-{top,right,bottom,left} | W? B? C? in any order (omitted fields reset to initials) | 0 none currentColor | no | all |
| border | W? B? C? in any order (resets all sides) | 0 none currentColor | no | all |
| border-{top-left,top-right,bottom-right,bottom-left}-radius | L (`%` relative to box) | 0 | no | all |
| border-radius | 1–4 L clockwise; elliptical `/` radii unsupported | 0 | no | all |
| outline-width / outline-style / outline-color | W / B / C | 0 / none / currentColor | no | all |
| outline | W? B? C? (resets all three) | 0 none currentColor | no | all |
| outline-offset | S (no %) | 0 | no | all |
| box-shadow | none or comma list of `[inset]? S S L? S? C? [inset]?`; omitted blur/spread = 0, color = currentColor | none | no | all |
| margin-{top,right,bottom,left} | S or A (%, em, rem allowed) | 0 | no | all |
| margin | 1–4 margin-side values | 0 | no | all |
| padding-{top,right,bottom,left} | L | 0 | no | all |
| padding | 1–4 L | 0 | no | all |
| width / height | L or A | auto | no | all |
| min-width / min-height | L or A | auto | no | all |
| max-width / max-height | L or A | auto | no | all |
| box-sizing | content-box or border-box | content-box | no | all |
| display | flex or block or none | block | no | all |
| flex-direction | row or column | row | no | flex containers |
| flex-wrap | nowrap or wrap | nowrap | no | flex containers |
| flex-grow / flex-shrink | N | 0 / 1 | no | flex items |
| flex-basis | L or A | auto | no | flex items |
| flex | none (= 0 0 auto), auto (= 1 1 auto), or N [N]? [L|A]? (omitted shrink=1, basis=0%) | 0 1 auto | no | flex items |
| gap / row-gap / column-gap | L (gap: 1 or 2 values row then column) | 0 | no | flex containers |
| justify-content | flex-start, flex-end, center, space-between, space-around, space-evenly | flex-start | no | flex containers |
| align-items / align-self | stretch, flex-start, flex-end, center (align-self also auto) | stretch / auto | no | flex containers / flex items |
| overflow / overflow-x / overflow-y | visible, hidden, scroll, auto (shorthand 1 or 2 x then y) | visible | no | all |
| font-family | comma list of quoted names or identifiers (multiword identifiers allowed); serif, sans-serif, monospace, cursive, fantasy, system-ui generic | sans-serif | yes | text and descendants |
| font-size | L (percent relative to parent's font size) | 16px | yes | text and descendants |
| font-weight | normal, bold, 100–900 in increments of 100 | 400 | yes | text and descendants |
| font-style | normal, italic, oblique | normal | yes | text and descendants |
| line-height | normal, nonnegative number, or L | normal | yes | text and descendants |
| text-align | start, end, left, right, center | start | yes | text and descendants |
| letter-spacing | normal or S (no %) | normal | yes | text and descendants |
| cursor | auto, default, pointer, text, not-allowed | auto | yes | all |
| accent-color | C | #3584e4 | yes | checkbox, radio and slider indicators |
| --* | arbitrary token sequence, including var() | absent | yes | all |

Only the listed properties and grammar are supported; e.g. transforms, transitions, grid, positioning, z-index, background images, font variants, viewport units, calc/min/max/clamp, and CSS-wide `revert` are unsupported. Unknown CSS is ignored with diagnostics as described above. Border radii affect painted boxes, but overflow clips use axis-aligned bounds rather than rounded clips. CSS `direction` is unsupported; sliders run left-to-right even when adjacent text is RTL. SVG and animated images are not decoded: only PNG and JPEG bytes are accepted, and other formats return a decode error. Text is grayscale, not LCD or SDF. Editors do not offer undo/redo, and no platform accessibility adapter is connected. The element types are `app`/`root`, `header`, `nav`, `main`, `section`, `aside`, `footer`, `button`, `input`, `textarea`, `checkbox`, `radio`, `slider`, `text`, `heading`, `image`, `icon`, `separator`, `spacer`, `row`, `column`, `stack`, `box`, `scroll`. UA: row/column use flex row/column; stack is a **distinct overlay layout** represented by type `stack` with block display (layout interprets its type; CSS display overrides layout); first child painted behind last, max child envelope, reverse hit order. Other elements start block. UA rules are overridable by author rules.

## Engine and caching

`css.Compile(ua, author Sheet) *Engine` builds an indexed cascade. `css.UA()` supplies the embedded user-agent sheet. Stylesheet inputs are compiled/copy-referenced at construction; do not mutate parsed sheets while compiling. To change CSS, compile a new engine. Selectors are indexed by the rightmost compound's ID, first class, type, or the universal bucket. Matching walks compiled selector compounds and the parent chain of memo nodes, not a caller-supplied element parent. A matching selector-list entry contributes its own specificity; source order resolves ties.

`css.ParseInline(src string) (*Declarations, []Diagnostic)` parses declarations (no braces). Pass the returned pointer as `Element.Inline`; treat it as immutable. `Element{Type, ID string; Classes []string; State StateFlags; Inline *Declarations}` is a per-call matching identity. State flags are `Hover`, `Active`, `Focus`, `FocusVisible`, `Disabled`. Classes are an unordered set; duplicate class entries are not expected. IDs/classes retain case, type is ASCII case-insensitive.

`Engine.Compute(e *Element, parent *Computed) *Computed` takes the result of the same engine for the parent, or nil for the root. `Computed.Style` points to the typed `Style`, with fixed fields matching the property table (sides, corners, `Length`, straight sRGB `Color`, keyword enums, numeric fields, font-family and shadow slices). `LineHeightKind` distinguishes `normal` and unitless `number` (`LineHeightNumber`) from length (`LineHeight`); `LetterSpacingNormal` distinguishes `normal` from zero. Length units are strings (`px`, `%`, `auto`); `em`/`rem` resolve to `px`. Never mutate returned styles or slices, the inline declarations, or CSS inputs; no compatibility string-map style exists. Custom properties are internal inherited token values; only declarations containing `var()` retain tokens for computed-time substitution. Shorthands containing `var()` share one pending substitution: after resolving variables the entire shorthand is expanded and validated before any of its longhand winners are computed. Invalid substitution resets every winning longhand of that shorthand to inherited or initial, rather than exposing partial results.

Cascade priority for normal declarations: UA < author < inline; for `!important`: author < inline, all important declarations win over normal (UA has no important). Within the same origin/importance, specificity then declaration source order wins. Invalid static declarations are dropped while compiling; invalid values after variable substitution invalidate the winning declaration at computed-value time, not the next cascade candidate.

The engine interns strings and unordered class sets per engine. A memo key is `(parent node pointer, type, ID, class-set ID, StateFlags, inline pointer)`; a memo hit returns the exact same `*Computed` without allocating. Parents' matching identities live on memo nodes. `EndFrame()` advances the generation and evicts entries not used in the last two frames, retaining ancestors needed by recently used descendants. Call it once after each frame, including frames without CSS changes. This engine is **single-goroutine only** and does not use locks: do not call `Compute` or `EndFrame` concurrently. The caller must retain the parent `Computed` through computation of children; a changed ancestor identity or inline pointer creates a new memo path for that subtree.

### Performance and verification

Engine benchmark numbers and budgets are in [performance.md](performance.md). Cold compute runs on first use or after a stylesheet change; a warm frame allocates nothing. A changed state recomputes exactly the affected subtree: `TestChangedStateMissesOnlySubtree` compares memo identities against the changed subtree size.
