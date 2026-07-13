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
		Usage:         wgpu.TextureUsageStorageBinding | wgpu.TextureUsageCopySrc,
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
