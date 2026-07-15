package gpu

import (
	"fmt"
	"image"

	"github.com/cogentcore/webgpu/wgpu"
)

type target struct {
	tex     *wgpu.Texture
	view    *wgpu.TextureView
	readbuf *wgpu.Buffer
	w, h    int
	bpr     uint32
}

func newTarget(d *Device, w, h int) (*target, error) {
	tex, err := d.device.CreateTexture(&wgpu.TextureDescriptor{
		Usage:         wgpu.TextureUsageStorageBinding | wgpu.TextureUsageCopySrc | wgpu.TextureUsageCopyDst,
		Dimension:     wgpu.TextureDimension2D,
		Size:          wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
		Format:        wgpu.TextureFormatRGBA8Unorm,
		MipLevelCount: 1,
		SampleCount:   1,
	})
	if err != nil {
		return nil, err
	}
	view, err := tex.CreateView(nil)
	if err != nil {
		tex.Release()
		return nil, err
	}
	bpr := align256(uint32(w) * 4)
	readbuf, err := d.device.CreateBuffer(&wgpu.BufferDescriptor{
		Size:  uint64(bpr) * uint64(h),
		Usage: wgpu.BufferUsageCopyDst | wgpu.BufferUsageMapRead,
	})
	if err != nil {
		view.Release()
		tex.Release()
		return nil, err
	}
	return &target{tex: tex, view: view, readbuf: readbuf, w: w, h: h, bpr: bpr}, nil
}

func (t *target) readRGBA(d *Device) (*image.RGBA, error) {
	enc, err := d.device.CreateCommandEncoder(nil)
	if err != nil {
		return nil, err
	}
	enc.CopyTextureToBuffer(
		t.tex.AsImageCopy(),
		&wgpu.ImageCopyBuffer{
			Buffer: t.readbuf,
			Layout: wgpu.TextureDataLayout{BytesPerRow: t.bpr, RowsPerImage: uint32(t.h)},
		},
		&wgpu.Extent3D{Width: uint32(t.w), Height: uint32(t.h), DepthOrArrayLayers: 1},
	)
	cmd, err := enc.Finish(nil)
	if err != nil {
		enc.Release()
		return nil, err
	}
	d.queue.Submit(cmd)
	cmd.Release()
	enc.Release()

	size := uint64(t.bpr) * uint64(t.h)
	done := false
	var status wgpu.BufferMapAsyncStatus
	if err := t.readbuf.MapAsync(wgpu.MapModeRead, 0, size, func(s wgpu.BufferMapAsyncStatus) {
		status = s
		done = true
	}); err != nil {
		return nil, err
	}
	for !done {
		d.device.Poll(true, nil)
	}
	if status != wgpu.BufferMapAsyncStatusSuccess {
		return nil, fmt.Errorf("gpu: readback map failed: %v", status)
	}

	data := t.readbuf.GetMappedRange(0, uint(size))
	img := image.NewRGBA(image.Rect(0, 0, t.w, t.h))
	for y := 0; y < t.h; y++ {
		src := data[y*int(t.bpr) : y*int(t.bpr)+t.w*4]
		copy(img.Pix[y*img.Stride:], src)
	}
	t.readbuf.Unmap()
	return img, nil
}

// writeRegion uploads a pixel rect of src into the texture, replacing whatever
// the compute pass left there. src is a full-frame image; only the rect is read.
func (t *target) writeRegion(d *Device, src *image.RGBA, x0, y0, x1, y1 int, scratch []byte) ([]byte, error) {
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return scratch, nil
	}
	stride := w * 4
	if cap(scratch) < stride*h {
		scratch = make([]byte, stride*h)
	}
	scratch = scratch[:stride*h]
	for y := 0; y < h; y++ {
		i := src.PixOffset(x0, y0+y)
		copy(scratch[y*stride:(y+1)*stride], src.Pix[i:i+stride])
	}
	return scratch, d.queue.WriteTexture(
		&wgpu.ImageCopyTexture{
			Texture: t.tex,
			Origin:  wgpu.Origin3D{X: uint32(x0), Y: uint32(y0)},
		},
		scratch,
		&wgpu.TextureDataLayout{BytesPerRow: uint32(stride), RowsPerImage: uint32(h)},
		&wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
	)
}

func (t *target) resize(d *Device, w, h int) error {
	if t.w == w && t.h == h {
		return nil
	}
	nt, err := newTarget(d, w, h)
	if err != nil {
		return err
	}
	t.release()
	*t = *nt
	return nil
}

func (t *target) release() {
	if t.readbuf != nil {
		t.readbuf.Release()
	}
	if t.view != nil {
		t.view.Release()
	}
	if t.tex != nil {
		t.tex.Release()
	}
}

func align256(v uint32) uint32 { return (v + 255) &^ 255 }
