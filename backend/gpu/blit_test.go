package gpu

import (
	"image"
	"testing"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/internal/sample"
)

// The window itself cannot be tested here — the sandbox and CI have no display,
// and the roadmap keeps the CI gate on the offscreen path. What can be tested is
// everything 6b adds to the pixels: the fullscreen triangle covers the frame,
// the texel fetch does not shift or flip it, and the format handling is right.
// Pointing the real blitter at an offscreen texture of the surface's format
// exercises all of that; only the swapchain acquire/present is left to on-device
// validation.

// blitTo runs the blitter into an offscreen stand-in for a swapchain image and
// reads the result back in RGBA order.
func blitTo(t *testing.T, r *Renderer, format wgpu.TextureFormat) *image.RGBA {
	t.Helper()
	return blitWithEntry(t, r, format, entryFor(format))
}

func blitWithEntry(t *testing.T, r *Renderer, format wgpu.TextureFormat, entry string) *image.RGBA {
	t.Helper()
	w, h := r.Size()

	dst, err := r.dev.device.CreateTexture(&wgpu.TextureDescriptor{
		// Exactly what a swapchain image offers, plus the CopySrc a real one
		// would not need: RenderAttachment, never StorageBinding.
		Usage:         wgpu.TextureUsageRenderAttachment | wgpu.TextureUsageCopySrc,
		Dimension:     wgpu.TextureDimension2D,
		Size:          wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
		Format:        format,
		MipLevelCount: 1,
		SampleCount:   1,
	})
	if err != nil {
		t.Fatalf("create %v texture: %v", format, err)
	}
	defer dst.Release()
	view, err := dst.CreateView(nil)
	if err != nil {
		t.Fatalf("create view: %v", err)
	}
	defer view.Release()

	b, err := newBlitterEntry(r.dev, format, entry)
	if err != nil {
		t.Fatalf("new blitter: %v", err)
	}
	defer b.release()

	if err := b.draw(r.dev, r.target.view, view); err != nil {
		t.Fatalf("blit: %v", err)
	}
	r.Sync()
	return readTexture(t, r.dev, dst, w, h, format)
}

func readTexture(t *testing.T, d *Device, tex *wgpu.Texture, w, h int, format wgpu.TextureFormat) *image.RGBA {
	t.Helper()
	bpr := align256(uint32(w) * 4)
	size := uint64(bpr) * uint64(h)
	buf, err := d.device.CreateBuffer(&wgpu.BufferDescriptor{
		Size:  size,
		Usage: wgpu.BufferUsageCopyDst | wgpu.BufferUsageMapRead,
	})
	if err != nil {
		t.Fatalf("create readback buffer: %v", err)
	}
	defer buf.Release()

	enc, err := d.device.CreateCommandEncoder(nil)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	enc.CopyTextureToBuffer(
		tex.AsImageCopy(),
		&wgpu.ImageCopyBuffer{
			Buffer: buf,
			Layout: wgpu.TextureDataLayout{BytesPerRow: bpr, RowsPerImage: uint32(h)},
		},
		&wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
	)
	cmd, err := enc.Finish(nil)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	d.queue.Submit(cmd)
	cmd.Release()
	enc.Release()

	done := false
	var status wgpu.BufferMapAsyncStatus
	if err := buf.MapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.BufferMapAsyncStatus) {
		status, done = s, true
	}); err != nil {
		t.Fatalf("map: %v", err)
	}
	for !done {
		d.device.Poll(true, nil)
	}
	if status != wgpu.BufferMapAsyncStatusSuccess {
		t.Fatalf("map failed: %v", status)
	}
	defer buf.Unmap()

	data := buf.GetMappedRange(0, uint(size))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(img.Pix[y*img.Stride:], data[y*int(bpr):y*int(bpr)+w*4])
	}
	// BGRA is a memory layout, not a shader-visible one: the fragment shader
	// writes .r to red either way. The swizzle only matters once the bytes are
	// read back as an image.RGBA.
	if format == wgpu.TextureFormatBGRA8Unorm || format == wgpu.TextureFormatBGRA8UnormSrgb {
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		}
	}
	return img
}

// rendered returns a renderer holding the sample scene, plus the offscreen
// pixels the blit is supposed to reproduce.
func rendered(t *testing.T) (*Renderer, *image.RGBA) {
	t.Helper()
	r, err := NewRenderer(sample.W, sample.H)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	if err := r.Render(sample.Scene()); err != nil {
		r.Release()
		t.Fatalf("render: %v", err)
	}
	want, err := r.ReadRGBA()
	if err != nil {
		r.Release()
		t.Fatalf("readback: %v", err)
	}
	return r, want
}

// A non-sRGB surface is the format pickFormat asks for precisely because the
// blit is then a copy. Anything but Δ=0 would mean the triangle, the fetch, or
// the row order is wrong — none of which have a tolerance to hide behind.
func TestBlitToUnormSurfaceIsExact(t *testing.T) {
	for _, format := range []wgpu.TextureFormat{
		wgpu.TextureFormatBGRA8Unorm,
		wgpu.TextureFormatRGBA8Unorm,
	} {
		t.Run(format.String(), func(t *testing.T) {
			r, want := rendered(t)
			defer r.Release()
			parity.Assert(t, blitTo(t, r, format), want, parity.Identical())
		})
	}
}

// The fallback path: the target already holds sRGB-encoded bytes, so the shader
// linearizes and the attachment re-encodes on write. The two are inverses and
// measured Δ=0 here, but the gate is the quantization floor, not Identical: this
// crosses a float pipeline (a WGSL pow against the driver's own encode), which is
// the contract's stated case for Δ≤1. Pinning it to 0 would be pinning Metal's
// pow, not the round trip's correctness.
func TestBlitToSRGBSurfaceRoundTrips(t *testing.T) {
	r, want := rendered(t)
	defer r.Release()
	parity.Assert(t, blitTo(t, r, wgpu.TextureFormatBGRA8UnormSrgb), want, parity.Quantized())
}

// Without the linearize, an sRGB surface would encode already-encoded bytes and
// wash the frame out. That is the bug the two shader entry points exist to
// prevent, so prove the wrong pairing is actually visible: if this ever passes,
// the sRGB test above proves nothing.
func TestSRGBSurfaceWouldWashOutWithoutLinearize(t *testing.T) {
	r, want := rendered(t)
	defer r.Release()

	// Same sRGB surface, but forced through the passthrough shader instead of
	// the linearizing one. The difference must be large and obvious.
	cfg := parity.Budget(8, "deliberate double sRGB encode; asserted to FAIL")
	naive := blitWithEntry(t, r, wgpu.TextureFormatBGRA8UnormSrgb, "fs_main")
	res, err := parity.Compare(naive, want, cfg)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if res.OK(cfg) {
		t.Fatalf("passthrough into an sRGB surface matched the target (%s): the double encode is not observable, so fs_main_srgb is untested", res)
	}
	t.Logf("double encode without linearize: %s", res)
}

func TestPickFormatPrefersNonSRGB(t *testing.T) {
	tests := []struct {
		name  string
		have  []wgpu.TextureFormat
		want  wgpu.TextureFormat
		fails bool
	}{{
		name: "prefers unorm when both offered",
		have: []wgpu.TextureFormat{wgpu.TextureFormatBGRA8UnormSrgb, wgpu.TextureFormatBGRA8Unorm},
		want: wgpu.TextureFormatBGRA8Unorm,
	}, {
		name: "takes srgb when it is all there is",
		have: []wgpu.TextureFormat{wgpu.TextureFormatBGRA8UnormSrgb},
		want: wgpu.TextureFormatBGRA8UnormSrgb,
	}, {
		name: "accepts rgba surfaces",
		have: []wgpu.TextureFormat{wgpu.TextureFormatRGBA8Unorm},
		want: wgpu.TextureFormatRGBA8Unorm,
	}, {
		name:  "no format at all",
		have:  nil,
		fails: true,
	}, {
		name:  "nothing 8-bit",
		have:  []wgpu.TextureFormat{wgpu.TextureFormatRGBA16Float},
		fails: true,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickFormat(wgpu.SurfaceCapabilities{Formats: tc.have})
			if tc.fails {
				if err == nil {
					t.Fatalf("pickFormat(%v) = %v, want error", tc.have, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("pickFormat(%v): %v", tc.have, err)
			}
			if got != tc.want {
				t.Fatalf("pickFormat(%v) = %v, want %v", tc.have, got, tc.want)
			}
		})
	}
}

func TestIsSRGB(t *testing.T) {
	for _, f := range []wgpu.TextureFormat{wgpu.TextureFormatBGRA8UnormSrgb, wgpu.TextureFormatRGBA8UnormSrgb} {
		if !isSRGB(f) {
			t.Errorf("isSRGB(%v) = false, want true", f)
		}
	}
	for _, f := range []wgpu.TextureFormat{wgpu.TextureFormatBGRA8Unorm, wgpu.TextureFormatRGBA8Unorm} {
		if isSRGB(f) {
			t.Errorf("isSRGB(%v) = true, want false", f)
		}
	}
}
