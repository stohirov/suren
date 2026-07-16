package gpu

import (
	_ "embed"
	"fmt"

	"github.com/cogentcore/webgpu/wgpu"
)

//go:embed blit.wgsl
var blitWGSL string

// The blit deliberately sits outside the gpupresent tag. It is the one piece
// 6b actually adds to the pixel path — format choice, sRGB handling, the
// texel-for-texel copy — and behind the tag it would be code CI can never run.
// A texture is a texture: pointed at a swapchain image it presents, pointed at
// an offscreen one of the same format it is testable headlessly. present.go
// keeps the parts that genuinely need a display.

// isSRGB reports whether writing to f applies an sRGB encode.
func isSRGB(f wgpu.TextureFormat) bool {
	return f == wgpu.TextureFormatBGRA8UnormSrgb || f == wgpu.TextureFormatRGBA8UnormSrgb
}

// pickFormat chooses a surface format and reports whether it is sRGB.
//
// A non-sRGB format is preferred because it makes the blit an exact passthrough:
// the target holds sRGB-encoded bytes already, so an sRGB attachment would
// encode them a second time and wash the frame out. When only an sRGB format is
// offered the shader undoes the encode instead — correct, but a pow() per pixel
// and a rounding step the passthrough does not have. Channel order needs no
// handling: BGRA8Unorm describes memory layout, while a fragment shader still
// writes .r to red, so the swizzle is the hardware's problem, not ours.
func pickFormat(caps wgpu.SurfaceCapabilities) (wgpu.TextureFormat, error) {
	if len(caps.Formats) == 0 {
		return 0, fmt.Errorf("gpu: surface reports no formats")
	}
	for _, f := range caps.Formats {
		if f == wgpu.TextureFormatBGRA8Unorm || f == wgpu.TextureFormatRGBA8Unorm {
			return f, nil
		}
	}
	for _, f := range caps.Formats {
		if isSRGB(f) {
			return f, nil
		}
	}
	return 0, fmt.Errorf("gpu: no 8-bit surface format among %v", caps.Formats)
}

// pickPresentMode chooses how the swapchain paces frames. Fifo is vsync and the
// only mode a surface must support, so it is both the default and the fallback.
//
// Unsynced exists because Fifo makes a frame-rate reading meaningless: it blocks
// until the display is ready, so a renderer finishing in 1 ms and one finishing
// in 8 ms both report exactly the refresh rate. Phase 6 is here to beat a
// ~14 ms/frame cost, and a number pinned to the monitor cannot show whether it
// did. Immediate presents as soon as the frame is done and tears, which is the
// right trade for measuring and the wrong one for looking at.
func pickPresentMode(caps wgpu.SurfaceCapabilities, unsynced bool) wgpu.PresentMode {
	if !unsynced {
		return wgpu.PresentModeFifo
	}
	for _, m := range caps.PresentModes {
		if m == wgpu.PresentModeImmediate {
			return m
		}
	}
	// Mailbox does not block on the display either; it just drops frames rather
	// than tearing, which still lets the renderer run flat out.
	for _, m := range caps.PresentModes {
		if m == wgpu.PresentModeMailbox {
			return m
		}
	}
	return wgpu.PresentModeFifo
}

type blitter struct {
	pipeline *wgpu.RenderPipeline
	layout   *wgpu.BindGroupLayout
}

// entryFor pairs the surface format with the fragment shader that keeps the
// blit a passthrough. Getting this pairing wrong is silent — a double-encoded
// frame renders, it just looks washed out — so blit_test.go pins both halves.
func entryFor(format wgpu.TextureFormat) string {
	if isSRGB(format) {
		return "fs_main_srgb"
	}
	return "fs_main"
}

func newBlitter(d *Device, format wgpu.TextureFormat) (*blitter, error) {
	return newBlitterEntry(d, format, entryFor(format))
}

func newBlitterEntry(d *Device, format wgpu.TextureFormat, entry string) (*blitter, error) {
	mod, err := d.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label:          "blit.wgsl",
		WGSLDescriptor: &wgpu.ShaderModuleWGSLDescriptor{Code: blitWGSL},
	})
	if err != nil {
		return nil, err
	}
	defer mod.Release()

	pipeline, err := d.device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "blit",
		Vertex: wgpu.VertexState{Module: mod, EntryPoint: "vs_main"},
		Primitive: wgpu.PrimitiveState{
			Topology:  wgpu.PrimitiveTopologyTriangleList,
			FrontFace: wgpu.FrontFaceCCW,
			CullMode:  wgpu.CullModeNone,
		},
		Multisample: wgpu.MultisampleState{Count: 1, Mask: 0xFFFFFFFF},
		Fragment: &wgpu.FragmentState{
			Module:     mod,
			EntryPoint: entry,
			// Replace, not blend: the target's alpha is the scene's, and
			// blending it against the swapchain would let the desktop show
			// through wherever the scene is translucent.
			Targets: []wgpu.ColorTargetState{{
				Format:    format,
				Blend:     &wgpu.BlendStateReplace,
				WriteMask: wgpu.ColorWriteMaskAll,
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	return &blitter{pipeline: pipeline, layout: pipeline.GetBindGroupLayout(0)}, nil
}

func (b *blitter) draw(d *Device, src, dst *wgpu.TextureView) error {
	group, err := d.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout:  b.layout,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, TextureView: src}},
	})
	if err != nil {
		return err
	}
	defer group.Release()

	enc, err := d.device.CreateCommandEncoder(nil)
	if err != nil {
		return err
	}
	pass := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View: dst,
			// Clear rather than Load: the triangle covers every pixel, so
			// there is nothing to preserve and Load would cost a read.
			LoadOp:  wgpu.LoadOpClear,
			StoreOp: wgpu.StoreOpStore,
		}},
	})
	pass.SetPipeline(b.pipeline)
	pass.SetBindGroup(0, group, nil)
	pass.Draw(3, 1, 0, 0)
	pass.End()
	pass.Release()

	cmd, err := enc.Finish(nil)
	if err != nil {
		enc.Release()
		return err
	}
	d.queue.Submit(cmd)
	cmd.Release()
	enc.Release()
	return nil
}

func (b *blitter) release() {
	if b.layout != nil {
		b.layout.Release()
		b.layout = nil
	}
	if b.pipeline != nil {
		b.pipeline.Release()
		b.pipeline = nil
	}
}
