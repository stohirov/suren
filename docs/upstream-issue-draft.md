# DRAFT — not filed. For review before submitting to cogentcore/webgpu.

**Title:** `Surface.GetCurrentTexture` discards `WGPUSurfaceTexture.status`, returning (NULL texture, nil error) on a failed acquire

**Repo:** github.com/cogentcore/webgpu · **Version:** v0.23.0 · **Platform found on:** darwin/arm64 (Metal, Apple M4)

---

## Summary

`Surface.GetCurrentTexture()` throws away `WGPUSurfaceTexture.status` and returns only
`ref.texture`. On a non-`Success` acquire — `Timeout`, `Outdated`, `Lost` — wgpu-native sets
`ref.texture` to NULL and reports the reason solely through `status`. Because that field is
dropped, the call returns a **non-nil `*Texture` wrapping a NULL handle, with a nil error**.

There is no way for a caller to detect this. `Texture.ref` is unexported and every accessor
dereferences it, so no nil-safe check is reachable from Go. The next call the caller makes —
`tex.CreateView(nil)`, per the type's own documented usage — passes NULL into wgpu-native and
**aborts the process**.

The status enum is already fully declared in the binding (`wgpu/enums.go:1293-1320`,
`SurfaceGetCurrentTextureStatus`, complete with a `String()` method). It is simply never read.

## The code path in v0.23.0

`wgpu/surface.go:12-18` — the C shim:

```c
static inline WGPUTexture gowebgpu_surface_get_current_texture(WGPUSurface surface, WGPUDevice device, void * error_userdata) {
	WGPUSurfaceTexture ref;
	wgpuDevicePushErrorScope(device, WGPUErrorFilter_Validation);
	wgpuSurfaceGetCurrentTexture(surface, &ref);
	wgpuDevicePopErrorScope(device, gowebgpu_error_callback_c, error_userdata);
	return ref.texture;   // <-- ref.status is discarded here
}
```

`wgpu/surface.go:106-128` — the Go wrapper:

```go
func (p *Surface) GetCurrentTexture() (*Texture, error) {
	// ... error scope callback ...
	ref := C.gowebgpu_surface_get_current_texture(p.ref, p.deviceRef, unsafe.Pointer(&errorCallbackHandle))
	if err != nil {
		if ref != nil {
			C.wgpuTextureRelease(ref)
		}
		return nil, err
	}
	return &Texture{p.deviceRef, ref}, nil   // <-- ref may be NULL; err is nil
}
```

**The error scope does not cover this case.** The shim pushes `WGPUErrorFilter_Validation`. An
outdated or timed-out swapchain is not a validation error, so `err` stays nil and the early
return never fires. The scope is load-bearing for genuine validation failures and should stay —
it is simply answering a different question than `status` does.

**And the NULL is unreachable from Go** (`wgpu/texture.go:33-36`):

```go
type Texture struct {
	deviceRef C.WGPUDevice
	ref       C.WGPUTexture   // unexported
}
```

So a caller cannot test `tex` for validity before using it. The two facts compose into an
unguardable abort: the binding says success, hands back a Texture, and the documented next call
kills the process.

## Impact

Any windowed application on this binding. Every reachable trigger is ordinary, not adversarial:

- a window minimised while rendering (Metal's `nextDrawable` blocks, then times out → `Timeout`)
- a window resized between `Configure` and acquire (→ `Outdated`)
- a display topology change, or GPU reset (→ `Lost`)

The failure mode is a process abort with no Go-side stack and no recoverable error — which is
the worst available outcome for what is, in wgpu's own model, an expected and recoverable
condition. `Outdated` in particular is *routine*: the documented response is to reconfigure the
surface and drop the frame.

## Reproduction

**Note on rigour: I have not run a reproducer that observes the abort.** The trigger requires a
real windowed surface entering a non-`Success` acquire, which I could not make deterministic
without racing the compositor. The bug is established here by code inspection of the path above,
which I believe is airtight; the sketch below is the shape I would expect to trigger it and is
offered as a starting point, not as a verified repro. I would rather say that than dress up an
untested snippet as one.

```go
// Sketch, UNTESTED. glfw + wgpu, surface configured normally.
for !win.ShouldClose() {
	glfw.PollEvents()

	// Minimise the window by hand here. On Metal, nextDrawable blocks ~1s,
	// then wgpu returns status=Timeout with texture=NULL.
	tex, err := surf.GetCurrentTexture()
	if err != nil {
		// never taken: Timeout is not a Validation error
		continue
	}
	view, err := tex.CreateView(nil) // <-- expected: abort inside wgpu-native, NULL handle
	if err != nil {
		continue
	}
	// ... render, present ...
	view.Release()
}
```

A maintainer with a repro harness can likely force `Outdated` more cheaply than `Timeout`: resize
the window without reconfiguring the surface, then acquire.

## Suggested fix

Return the status alongside the texture. The enum already exists, so this is plumbing rather than
design. Minimal shape, preserving the existing error scope:

```c
static inline WGPUTexture gowebgpu_surface_get_current_texture(WGPUSurface surface, WGPUDevice device, void * error_userdata, WGPUSurfaceGetCurrentTextureStatus * out_status) {
	WGPUSurfaceTexture ref;
	wgpuDevicePushErrorScope(device, WGPUErrorFilter_Validation);
	wgpuSurfaceGetCurrentTexture(surface, &ref);
	wgpuDevicePopErrorScope(device, gowebgpu_error_callback_c, error_userdata);
	*out_status = ref.status;
	return ref.texture;
}
```

Three options for the Go surface, in the order I'd prefer them:

1. **A new method, no break:**
   `GetCurrentTextureWithStatus() (*Texture, SurfaceGetCurrentTextureStatus, error)`, with
   `GetCurrentTexture` delegating to it and keeping today's signature. Additive; callers who
   need to recover opt in.
2. **Make the existing method safe:** have `GetCurrentTexture` return a non-nil error when
   `status != Success` (wrapping the status, e.g. via a `SurfaceAcquireError` type that exposes
   it). This is a behaviour change, but it changes a process abort into an error — every caller
   affected by the change is a caller that was previously aborting. Strictly, the current
   contract cannot be depended on.
3. **At minimum, never hand back a NULL:** if `ref.texture == nil`, return `(nil, err)` with a
   non-nil error even if the status itself is not surfaced. This loses the reason for the
   failure — a caller cannot distinguish "reconfigure and retry" (`Outdated`) from "give up"
   (`DeviceLost`) — but it removes the abort, and it is a two-line change.

Option 1 or 2 also makes `Outdated` handleable, which is the case that actually matters: it has a
correct, documented response (reconfigure, drop the frame) that callers cannot currently
implement because they cannot detect it.

I'm happy to send a PR for whichever shape you prefer.

## Workaround, for anyone who finds this first

Do not acquire from a window that cannot have a drawable. In our renderer, refusing to acquire
while the window is iconified removes the only trigger we could actually reach:

```go
if p.win.GetAttrib(glfw.Iconified) == glfw.True {
	return nil // no drawable; acquiring would time out, and a timeout is unreportable
}
tex, err := p.surf.GetCurrentTexture()
```

This is a mitigation, not a fix — a slow acquire under heavy load remains exposed, and there is
nothing a caller can do about `Outdated` at all.
