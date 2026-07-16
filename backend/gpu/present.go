//go:build gpupresent

// Native windowed present (Phase 6b). Quarantined behind the gpupresent tag so
// the default build, the headless CI test binary, and every package that merely
// renders offscreen never link glfw or a platform display framework.
//
// 6a drove a window by reading the frame back to the CPU and handing the pixels
// to Ebiten. This drops the readback: compute writes the target, the blit render
// pass copies it into the swapchain image, and the frame is presented from GPU
// memory without ever crossing the bus.
//
// Only the window plumbing lives here. The blit itself is in blit.go, untagged,
// so CI can test the pixel path without a display.

package gpu

import (
	"fmt"
	"runtime"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/cogentcore/webgpu/wgpuglfw"
	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

// glfw must be called from the thread that initialised it, and on macOS that
// has to be the main thread. This runs during package init, on the main
// goroutine, before any user code gets a chance to migrate off it.
func init() { runtime.LockOSThread() }

// live counts open Presenters, because glfw.Init/Terminate are process-wide
// while Release is per-Presenter: Terminate destroys *every* remaining window,
// so a second Presenter's Release would silently invalidate the first's window
// and leave its surface pointing at a freed layer. Terminate therefore only
// fires for the last one out.
//
// A plain int is enough: every glfw call here is already required to be on the
// main thread, so there is no second goroutine to race with.
var live int

// Presenter owns a window, its surface, and the renderer feeding it.
type Presenter struct {
	win  *glfw.Window
	surf *wgpu.Surface
	dev  *Device // the device r owns; held so teardown can be ordered
	cfg  wgpu.SurfaceConfiguration
	r    *Renderer
	blit *blitter
}

// Options tunes how a Presenter drives its surface.
type Options struct {
	// Unsynced drops the vsync cap when the surface offers a mode without one,
	// so the frame rate measures the renderer instead of the display. It tears;
	// it is for taking a number, not for looking at.
	Unsynced bool

	// Ready, if set, runs once the surface is configured and before the first
	// frame. It is what makes RunPresent usable for anything that needs to know
	// what was negotiated — format, present mode, framebuffer size — none of
	// which a caller may assume, and none of which RunPresent otherwise exposes.
	Ready func(*Presenter)
}

// NewPresenter opens a window sized w×h in screen points and builds a renderer
// that draws at its full framebuffer resolution, presenting on vsync.
func NewPresenter(title string, w, h int) (*Presenter, error) {
	return NewPresenterWith(title, w, h, Options{})
}

// NewPresenterWith is NewPresenter with the surface behaviour spelled out.
func NewPresenterWith(title string, w, h int, o Options) (*Presenter, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("gpu: glfw init: %w", err)
	}
	// NoAPI is what makes this a wgpu window: glfw would otherwise create a GL
	// context that we neither use nor want linked.
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	win, err := glfw.CreateWindow(w, h, title, nil, nil)
	if err != nil {
		if live == 0 {
			glfw.Terminate()
		}
		return nil, fmt.Errorf("gpu: create window: %w", err)
	}
	live++

	// newDeviceForSurface cleans up its own instance/surface on failure, so
	// there is nothing here but the window.
	dev, surf, err := newDeviceForSurface(wgpuglfw.GetSurfaceDescriptor(win))
	if err != nil {
		win.Destroy()
		glfw.Terminate()
		return nil, err
	}

	p := &Presenter{win: win, surf: surf, dev: dev}
	caps := surf.GetCapabilities(dev.adapter)
	format, err := pickFormat(caps)
	if err != nil {
		p.Release()
		return nil, err
	}
	fw, fh := win.GetFramebufferSize()
	p.cfg = wgpu.SurfaceConfiguration{
		Usage:       wgpu.TextureUsageRenderAttachment,
		Format:      format,
		Width:       uint32(fw),
		Height:      uint32(fh),
		PresentMode: pickPresentMode(caps, o.Unsynced),
		AlphaMode:   wgpu.CompositeAlphaModeAuto,
	}

	// Render at framebuffer resolution, not window points: on a Retina display
	// those differ by the scale factor, and matching the framebuffer is both
	// what keeps the blit 1:1 and the whole point of a GPU rasterizer.
	p.r, err = newRendererOnDevice(dev, fw, fh)
	if err != nil {
		p.Release()
		return nil, err
	}
	p.blit, err = newBlitter(dev, format)
	if err != nil {
		p.Release()
		return nil, err
	}
	surf.Configure(dev.adapter, dev.device, &p.cfg)
	if o.Ready != nil {
		o.Ready(p)
	}
	return p, nil
}

// Renderer exposes the renderer driving the window, for callers that want its
// size or timing counters.
func (p *Presenter) Renderer() *Renderer { return p.r }

// Format reports the surface format actually negotiated. The roadmap asked for
// this to be recorded rather than assumed — it varies by platform, and whether
// it is sRGB decides which blit shader runs.
func (p *Presenter) Format() wgpu.TextureFormat { return p.cfg.Format }

// PresentMode reports the pacing the surface actually accepted, which is not
// always the one asked for: Unsynced falls back to vsync on a surface that
// offers nothing else, and a frame-rate reading taken without checking this
// would be reporting the display's refresh as the renderer's speed.
func (p *Presenter) PresentMode() wgpu.PresentMode { return p.cfg.PresentMode }

// Size reports the framebuffer size the renderer is currently drawing at.
func (p *Presenter) Size() (w, h int) { return p.r.Size() }

// ContentScale is framebuffer pixels per window point — 2 on the Retina display
// this was built against. Scenes authored in points should scale by it to fill
// the window.
//
// Both sizes are zero for a minimised window, and 1 is the right answer there:
// the ratio is meaningless, and returning the honest 0 would scale a scene down
// to nothing on the way back.
func (p *Presenter) ContentScale() float64 {
	w, _ := p.win.GetSize()
	fw, _ := p.win.GetFramebufferSize()
	if w <= 0 || fw <= 0 {
		return 1
	}
	return float64(fw) / float64(w)
}

// ShouldClose reports whether the user has asked to close the window.
func (p *Presenter) ShouldClose() bool { return p.win.ShouldClose() }

// Close asks the present loop to stop, exactly as closing the window would. It
// is how a caller inside RunPresent's frame callback ends the run, and it makes
// the loop — and the teardown after it — reachable without a human at the mouse.
func (p *Presenter) Close() { p.win.SetShouldClose(true) }

// Frame renders one scene and presents it. It polls window events, follows a
// resize, and returns once the frame is queued for display.
func (p *Presenter) Frame(s *scene.Scene) error {
	glfw.PollEvents()
	// Do not acquire from a minimised window. This is not an optimisation: a
	// minimised window has no drawable, Metal's nextDrawable blocks and then
	// times out, and a timed-out acquire is the case this binding cannot report
	// (see below). Not asking is the only reliable way not to be lied to.
	if p.win.GetAttrib(glfw.Iconified) == glfw.True {
		return nil
	}
	if err := p.syncSize(); err != nil {
		return err
	}
	if err := p.r.Render(s); err != nil {
		return err
	}

	// KNOWN GAP, and it is the library's, not this code's. wgpu reports acquire
	// failure through WGPUSurfaceTexture.status (Timeout/Outdated/Lost), and
	// cogentcore/webgpu v0.23.0's C shim returns only `ref.texture` and throws
	// the status away; its error scope catches Validation errors, which an
	// outdated swapchain is not. So a failed acquire arrives here as
	// (non-nil *Texture wrapping a NULL handle, nil error), the err branch
	// below never fires for it, and CreateView would hand the NULL to
	// wgpu-native and abort the process.
	//
	// It cannot be checked from here either: Texture.ref is unexported and
	// every getter dereferences it, so there is no nil-safe accessor to ask.
	// The Iconified guard above removes the trigger this project can actually
	// reach; a >1s stall under extreme load remains theoretically exposed, and
	// the honest fix is upstream — the binding must surface the status. Kept as
	// a real error path rather than deleted because it is correct for the
	// Validation errors the scope *does* catch.
	tex, err := p.surf.GetCurrentTexture()
	if err != nil {
		// A swapchain the compositor invalidated. Reconfiguring and dropping
		// this frame is the recovery; the next one acquires a fresh image.
		p.surf.Configure(p.r.dev.adapter, p.r.dev.device, &p.cfg)
		return nil
	}
	view, err := tex.CreateView(nil)
	if err != nil {
		return err
	}
	// Only the view is released: the surface owns the texture it handed out.
	defer view.Release()

	if err := p.blit.draw(p.r.dev, p.r.target.view, view); err != nil {
		return err
	}
	p.surf.Present()
	return nil
}

// syncSize keeps surface and target the same size as the framebuffer. Both must
// move together — the blit is 1:1, so a mismatch would show as a cropped or
// padded frame rather than a scaled one.
func (p *Presenter) syncSize() error {
	fw, fh := p.win.GetFramebufferSize()
	// A minimised window reports a zero framebuffer, which is not a valid
	// surface configuration. Hold the last good size until it comes back.
	if fw <= 0 || fh <= 0 {
		return nil
	}
	if uint32(fw) == p.cfg.Width && uint32(fh) == p.cfg.Height {
		return nil
	}
	if err := p.r.Resize(fw, fh); err != nil {
		return err
	}
	p.cfg.Width, p.cfg.Height = uint32(fw), uint32(fh)
	p.surf.Configure(p.r.dev.adapter, p.r.dev.device, &p.cfg)
	return nil
}

// Release tears the presenter down. Order is the point, and it is the reason
// there is one path rather than an unwind per constructor failure:
//
// The surface must go before the instance that created it. Renderer.Release
// bundles queue+device+adapter+instance, so releasing the renderer first would
// take the instance out from under a live surface — the library's own example
// is careful to release the surface before the instance, and this is the same
// constraint. The window goes last: the surface was made from its layer.
//
// It is also safe on a half-built Presenter, which is what the constructor's
// error paths rely on: every field is nil-checked, and Device.Release zeroes
// itself, so the dev/renderer double-release is a no-op rather than a fault.
func (p *Presenter) Release() {
	if p.blit != nil {
		p.blit.release()
		p.blit = nil
	}
	if p.surf != nil {
		p.surf.Release()
		p.surf = nil
	}
	if p.r != nil {
		p.r.Release() // releases the device
		p.r = nil
	}
	if p.dev != nil {
		p.dev.Release() // no-op when the renderer already did it
		p.dev = nil
	}
	if p.win != nil {
		p.win.Destroy()
		p.win = nil
		if live--; live == 0 {
			glfw.Terminate()
		}
	}
}

// RunPresent opens a window and draws frame into it until the window closes.
// The canvas is pre-scaled by the content scale, so a scene authored in points
// against the w×h it was given covers the window on a Retina display rather
// than a quarter of it.
//
// The scale it applies is the DPI ratio and nothing else, so a scene keeps the
// extent it was authored at when the window is resized: grow the window and the
// uncovered region is the blit's clear colour, shrink it and the scene crops.
// That matches window.Run, and it is a real limit of this signature — the
// callback is handed a canvas and never learns the new size. A scene that must
// follow the window should drive a Presenter directly and read Size() per frame.
func RunPresent(title string, w, h int, frame func(*render.Canvas)) error {
	return RunPresentWith(title, w, h, Options{}, frame)
}

// RunPresentWith is RunPresent with the surface behaviour spelled out.
func RunPresentWith(title string, w, h int, o Options, frame func(*render.Canvas)) error {
	p, err := NewPresenterWith(title, w, h, o)
	if err != nil {
		return err
	}
	defer p.Release()

	c := render.NewCanvas()
	for !p.ShouldClose() {
		c.Reset()
		if s := p.ContentScale(); s != 1 {
			c.Scale(s, s)
		}
		frame(c)
		if err := p.Frame(c.Scene()); err != nil {
			return err
		}
	}
	return nil
}

// newDeviceForSurface builds a device around an existing surface. Order matters:
// the surface is created first so RequestAdapter can filter for an adapter that
// can present to it. NewDeviceOn cannot do this — it has no window to bind.
func newDeviceForSurface(sd *wgpu.SurfaceDescriptor) (*Device, *wgpu.Surface, error) {
	inst := wgpu.CreateInstance(nil)
	if inst == nil {
		return nil, nil, fmt.Errorf("gpu: create instance failed")
	}
	surf := inst.CreateSurface(sd)
	if surf == nil {
		inst.Release()
		return nil, nil, fmt.Errorf("gpu: create surface failed")
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference:   wgpu.PowerPreferenceHighPerformance,
		CompatibleSurface: surf,
	})
	if err != nil {
		surf.Release()
		inst.Release()
		return nil, nil, fmt.Errorf("gpu: request adapter for surface: %w", err)
	}
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		adapter.Release()
		surf.Release()
		inst.Release()
		return nil, nil, fmt.Errorf("gpu: request device: %w", err)
	}
	return &Device{instance: inst, adapter: adapter, device: device, queue: device.GetQueue()}, surf, nil
}
