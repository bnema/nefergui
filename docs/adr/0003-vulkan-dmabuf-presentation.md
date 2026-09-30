# ADR 0003 — Vulkan presentation through DMA-BUF

Status: accepted

## Decision

NeferGUI renders with Vulkan into exportable images and presents them as `zwp_linux_dmabuf_v1` buffers. It does not use `VK_KHR_swapchain`.

- Synchronization uses `wp_linux_drm_syncobj_v1` with Vulkan timeline semaphores. There is no CPU-fence fallback.
- The Vulkan physical device must match the `main_device` of linux-dmabuf feedback. There is no cross-GPU fallback.
- Formats: one-plane `XRGB8888` for opaque windows (default) and `ARGB8888` for transparent windows, both mapped to `VK_FORMAT_B8G8R8A8_UNORM`, with a modifier supported by both the compositor and the GPU.
- Three exportable images per window.
- Shaders are GLSL compiled by `glslc` through `go generate`. SPIR-V is committed and embedded; consumers do not need `glslc`.

## Buffer lifecycle

```text
Available → Rendering → acquire point exported → committed with acquire/release points
→ compositor reads → release point of its latest commit reached → Available
```

While a `wp_linux_drm_syncobj_surface_v1` exists, `wl_buffer.release` delivery is undefined (linux-drm-syncobj-v1). A buffer is reused only after the release point set for its most recent commit is signaled; each buffer has its own release timeline because points may signal out of order. `wl_buffer.release` is traced when it arrives but never gates reuse. Frame callbacks schedule drawing; they never authorize reuse.

The UI loop never blocks in Wayland dispatch while GPU or compositor completion is pending. Blocking sources (Wayland socket, release timeline waits) run on their own goroutines and only post events to the loop, which owns all presentation state. Resize creates a new generation; old images are retired after their latest release point signals.

At shutdown, release points protect reuse of contents, **not** the lifetime of exported DMA-BUF memory: kernel FD references and compositor imports are independent of the client's Vulkan allocation. Detach and commit null, destroy the syncobj surface, wait at most 200 ms for outstanding release points for tracing, wait for client GPU completion, destroy `wl_buffer`s and **all** client images/memory/semaphores/syncobj handles, then destroy the device. Never abandon Vulkan allocations solely because a compositor release point has not arrived.

`Opaque()` sets a full-surface opaque region. `Transparent()` uses premultiplied alpha and no opaque region. Background blur is a compositor setting, not a NeferGUI feature.
