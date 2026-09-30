# Provenance of vendored bidi implementation

Source: `golang.org/x/text@v0.42.0/unicode/bidi` (BSD-3-Clause; `LICENSE`). Copied unchanged except, in `bidi.go`, the package import comment and the upstream `go:generate` directive (its generators are not vendored), both marked `// nefergui:`. `levels.go` and `conformance_test.go` are NeferGUI additions; all modifications/extensions are marked `// nefergui:`. Upstream generator and generated-table tests depend on `golang.org/x/text/internal/*` (inaccessible from NeferGUI); they are deliberately not copied. Upstream `bidi_test.go` is retained and passing. Tables selected by Go 1.27 use Unicode **17.0.0**; `tables15.0.0.go` is retained for the original build tags. Go 1.27 is the supported module version.

SHA-256 of each original copied file (before the marked package-comment modification):

| file | SHA-256 |
| --- | --- |
| bidi.go | b1e9360e9a026006f56914a5716b18780346418bb0b64bce23a4ff92d8147b13 |
| bidi_test.go | 2e01617e4fc2f617bfd0246852c398525ae28616d05bf3c1d28d8c0dcb574d87 |
| bracket.go | 5caa24f9c4e04a54d18875468f976b4b7c09c8700e88286f2fd96828f134107e |
| core.go | ef15872f0cac7702bba67bbba334c4fc85376869e18fadec40e646f1ba8c4493 |
| prop.go | f6391b2f69a1ae2a0ac1f0599a90b260ec350a19f73ad5d6572660100b5950bc |
| trieval.go | bf3e17fca178c7d13139aa7cb7828005a0a1b8f4dca8ef97966236c6469a2136 |
| tables15.0.0.go | 98c2d6d57e116667e73a4f5c89ee20ead7b27b50d561b5c5d23d8ebf0e3f3310 |
| tables17.0.0.go | a49eafd4eb11f7c8ae810ee5d69ca59a0cddd97dc644afbcb3e40563617088fc |
| LICENSE | 911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad |

Unicode 17 `BidiCharacterTest.txt`: https://www.unicode.org/Public/17.0.0/ucd/BidiCharacterTest.txt; SHA-256 `a3e6e905ab5afbe318a96df5401d0372a04cd73ef139ab5e3cf0ae241c255488`. Terms: https://www.unicode.org/license.txt (copied as `testdata/LICENSE`; SHA-256 `e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96`). All 91,707 cases must pass for paragraph level, resolved L1 levels, and L2 order. NeferGUI's wrapper canonicalizes the U+2329/U+232A bracket identifiers to U+3008 before calling the upstream bracket pairer (4 otherwise failing canonical-equivalence cases); the upstream algorithm is otherwise unchanged.
