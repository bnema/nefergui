# Pinned font fixtures

Copied byte-for-byte from the Arch Linux distribution packages listed below; upstream project links are the corresponding canonical source URLs. The SHA-256 values pin these exact packaged files (not an unverified upstream archive). No runtime dependency on distribution fonts. Total font payload ~4.9 MB. Licenses are included alongside.

| File | Upstream | Distribution version | SHA-256 | License |
| --- | --- | --- | --- | --- |
| NotoSans-Regular.ttf | https://github.com/notofonts/latin-greek-cyrillic | noto-fonts 2026.09.01-1 | 478c558ea716033cd60c03438f628dfa75694dcf6b5f6d505a2f05fd2b4f3823 | Apache-2.0 (`LICENSE-Noto.txt`) |
| NotoSansArabic-Regular.ttf | https://github.com/notofonts/arabic | noto-fonts 2026.09.01-1 | bdff3e5659d67e67def05b33f749683b9376ae819d65d3dd62ac4640b3aaef48 | Apache-2.0 (`LICENSE-Noto.txt`) |
| NotoSansDevanagari-Regular.ttf | https://github.com/notofonts/devanagari | noto-fonts 2026.09.01-1 | 306b53ecfb182a504dd8a7446093c316387d2fd8dc350d0792ed1753fe0996cd | Apache-2.0 (`LICENSE-Noto.txt`) |
| NotoSansSymbols2-Regular.ttf | https://github.com/notofonts/symbols | noto-fonts 2026.09.01-1 | c4a0a80f0041ce4be81e2478faad22776d23edb98ae3f0d19bd37044820ecf9d | Apache-2.0 (`LICENSE-Noto.txt`) |
| IBMPlexSansJP-Regular.ttf | https://github.com/IBM/plex | ttf-ibm-plex 6.4.0-2 | 25d96fe620f12fba6cf09158807117ec13d1e7c9debff488117cf21dc8844688 | OFL-1.1 (`LICENSE-Plex.txt`) |
| AdwaitaSans-Regular.ttf | https://gitlab.gnome.org/GNOME/adwaita-fonts | adwaita-fonts 51.0-2 | 8381c33b9a44f066f2b99dba3d416a2342891e28c956a35dfd8d16ee2987e6d4 | OFL-1.1 (`LICENSE-Adwaita.txt`); single font containing a variable face (wght/opsz axes) |

For the variable font test, the last file has one face and `fvar` axes (wght and opsz). The default coordinates are used for static tests; `wght=700` is used for the variable test. CJK coverage is Japanese Kanji in IBM Plex Sans JP (not all of Unicode Han). Symbols 2 provides monochrome outlines for some pictographs, *not* full emoji coverage; missing sequences render `.notdef` tofu. Bitmap-only color emoji fonts are deliberately excluded.
