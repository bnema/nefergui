# PENDING human approval — CPU-only text reference images

These four PNGs are CPU composited grayscale glyph masks from the pinned fonts.
They are **not** Vulkan/output golden files and have not been human approved.
Do not move to `../approved` without visual review against intended samples and
subsequent GPU linear-light composition checks. Tests compare with approved
images **only when present**; metric and outline-area tests run unconditionally.

Images show `office fi سلام कि 日本語 ★` using Noto Sans at logical 20px and
scales 1, 1.25, 1.5, 2. Rendered with source-over black on white at integer
mask origins and 4-step horizontal subpixel phases; Y phase is integer.
