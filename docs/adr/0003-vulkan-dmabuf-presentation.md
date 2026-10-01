# ADR 0003 — Vulkan presentation through DMA-BUF

Status: accepted

## Decision

NeferGUI renders with Vulkan into exportable images and hands them to the application as DMA-BUF buffers (`Renderer`, see ADR 0007). The application presents them through `zwp_linux_dmabuf_v1`. NeferGUI does not use `VK_KHR_swapchain`.

- Synchronization uses DRM syncobj timelines (`wp_linux_drm_syncobj_v1` on the Wayland side) with Vulkan timeline semaphores. There is no CPU-fence fallback.
- The Vulkan physical device must match the `main_device` of linux-dmabuf feedback, passed in `RendererConfig.MainDevice`. There is no cross-GPU fallback.
- Formats: one-plane `XRGB8888` by default and `ARGB8888` when `RendererConfig.Transparent` is set, both mapped to `VK_FORMAT_B8G8R8A8_UNORM`, with a modifier supported by both the compositor (`RendererConfig.Formats`) and the GPU.
- Three exportable images per Renderer, re-created on resize.
- Shaders are GLSL compiled by `glslc` through `go generate`. SPIR-V is committed and embedded; consumers do not need `glslc`.

## Buffer lifecycle

```text
Available → Rendering → acquire point exported → Output returned by Render
→ compositor reads → release point of its latest commit signaled → Released → Available
```

Each buffer has its own release timeline, because points may signal out of order. After each `Render` the Renderer arms an eventfd on the release point of that commit (`DRM_IOCTL_SYNCOBJ_EVENTFD`, one-shot). `Output.ReleaseFD` is that eventfd. When it becomes readable the application calls `Released(buffer)`, which reads the eventfd and returns the buffer to the pool. A buffer is reused only after the release point set for its most recent commit has signaled; `wl_buffer.release` never gates reuse, because its delivery is undefined while a syncobj surface exists. Frame callbacks schedule drawing; they never authorize reuse.

The Renderer starts no goroutine and never blocks on the GPU or the compositor. `Render` returns false when no buffer is free; `Pending()` reports a built frame that waits for the GPU rather than the compositor, and the application retries after a short timer. Resize creates a new generation: buffers of the old one are reported in `Output.Retired` once their release point has signaled, and their descriptors stay open until the next `Render` or `Close`.

All descriptors in `Output` (DMA-BUF planes, timelines, eventfds) stay owned by the Renderer. The application duplicates them before a transport consumes them.

## Shutdown

Release points protect reuse of contents, **not** the lifetime of exported DMA-BUF memory: kernel descriptor references and compositor imports are independent of the client's Vulkan allocation. `Close` waits for client GPU completion, then frees **all** images, memory, semaphores, syncobj handles and eventfds, and destroys the device. It never abandons Vulkan allocations because a compositor release point has not arrived. The application detaches and destroys its own Wayland buffers and syncobj surface before or after `Close`; the Renderer does not know about them. `Close` also returns any Vulkan validation errors recorded while `NEFERGUI_VULKAN_VALIDATION=1` was set.

Opaque or transparent regions, background blur and cursor mechanics belong to the application: `Output.InputRects` and `Output.Cursor` carry the requests, and `RendererConfig.Transparent` selects premultiplied alpha output.
