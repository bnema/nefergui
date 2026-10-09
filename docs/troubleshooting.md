# Troubleshooting

- Wayland connection and missing globals are reported by the application's Wayland client, not by NeferGUI. The compositor must offer linux-dmabuf and linux-drm-syncobj.
- No suitable GPU: `no Vulkan physical devices; need DRM main_device ...`, `no Vulkan DRM device matches linux-dmabuf main_device ...`, or `matching Vulkan device missing ...` means the Vulkan driver cannot serve the compositor's DRM device. `no one-plane exportable B8G8R8A8 modifier shared with compositor ...` means they have no compatible modifier. Check driver and compositor support; there is no cross-GPU fallback.
- Fonts: `nefergui: fonts: no usable fonts: ...` or `nefergui: no usable fonts` means system font discovery failed. Install usable TrueType/OpenType system fonts.
- `NEFERGUI_VULKAN_VALIDATION=1` enables Vulkan validation; an unavailable validation layer reports `validation layer VK_LAYER_KHRONOS_validation unavailable`. Install the validation layer or unset the variable. Broken implicit Vulkan layers can prevent startup: use `VK_LOADER_LAYERS_DISABLE=<layer>` to disable the offending layer for diagnosis.
- Build with `-tags nefergui_debug` for identity diagnostics (duplicate keys, unkeyed type changes, stale handles).
- To regenerate committed SPIR-V, install `glslc` and run `go generate ./...`; consumers do not need `glslc`.
- If a requested font cannot be opened or parsed, text selection skips that face and tries the next matching face. Inspect catalog diagnostics for the failed path; system font discovery uses XDG font directories and does not require fontconfig.
- System fonts are indexed on demand: at startup only files whose name matches a requested or default family (for example `NotoSans-Regular.ttf` for `Noto Sans`). A family that no file name reveals, or a glyph the named families lack, indexes every remaining font once, in parallel. Name a family that is installed under a matching file name to keep the first frame fast.
