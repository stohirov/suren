package gpu

import (
	"image"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

var _ render.Renderer = (*Renderer)(nil)

type Renderer struct {
	dev    *Device
	w, h   int
	target *target
	ras    *rasterizer

	segBuf    *wgpu.Buffer
	nodeBuf   *wgpu.Buffer
	stopBuf   *wgpu.Buffer
	tileOff   *wgpu.Buffer
	tileNode  *wgpu.Buffer
	tileSegOf *wgpu.Buffer
	tileSegIx *wgpu.Buffer
	clipsBuf  *wgpu.Buffer
	nx, ny    int

	enc        *Encoded
	lastFP     uint64
	haveFrame  bool
	dispatches int
}

func NewRenderer(w, h int) (*Renderer, error) { return NewRendererOn(Any, w, h) }

// NewRendererOn pins the renderer to one native backend. It exists for the
// portability harness (Phase 12d); production takes whatever NewRenderer picks.
func NewRendererOn(b Backend, w, h int) (*Renderer, error) {
	dev, err := NewDeviceOn(b)
	if err != nil {
		return nil, err
	}
	t, err := newTarget(dev, w, h)
	if err != nil {
		dev.Release()
		return nil, err
	}
	ras, err := newRasterizer(dev, w, h)
	if err != nil {
		t.release()
		dev.Release()
		return nil, err
	}
	return &Renderer{dev: dev, w: w, h: h, target: t, ras: ras, enc: &Encoded{}}, nil
}

func (r *Renderer) Device() *Device { return r.dev }

func (r *Renderer) Size() (w, h int) { return r.w, r.h }

func (r *Renderer) Resize(w, h int) error {
	if r.w == w && r.h == h {
		return nil
	}
	if err := r.target.resize(r.dev, w, h); err != nil {
		return err
	}
	r.w, r.h = w, h
	r.ras.w, r.ras.h = w, h
	r.haveFrame = false
	return nil
}

func (r *Renderer) Render(s *scene.Scene) error {
	EncodeInto(r.enc, s, r.w, r.h)
	if r.haveFrame && r.enc.Fingerprint == r.lastFP {
		return nil
	}
	if err := r.upload(r.enc); err != nil {
		return err
	}
	if err := r.ras.run(r.dev, r.target, r.segBuf, r.nodeBuf, r.tileOff, r.tileNode, r.stopBuf, r.tileSegOf, r.tileSegIx, r.clipsBuf, r.nx, r.ny); err != nil {
		return err
	}
	r.lastFP = r.enc.Fingerprint
	r.haveFrame = true
	r.dispatches++
	return nil
}

func (r *Renderer) ReadRGBA() (*image.RGBA, error) { return r.target.readRGBA(r.dev) }

func (r *Renderer) Sync() {
	for !r.dev.device.Poll(true, nil) {
	}
}

func (r *Renderer) upload(e *Encoded) error {
	r.nx, r.ny = e.NTilesX, e.NTilesY
	if err := r.storage(&r.segBuf, wgpu.ToBytes(e.Segments)); err != nil {
		return err
	}
	if err := r.storage(&r.nodeBuf, wgpu.ToBytes(e.Nodes)); err != nil {
		return err
	}
	if err := r.storage(&r.stopBuf, wgpu.ToBytes(e.Stops)); err != nil {
		return err
	}
	if err := r.storage(&r.tileOff, wgpu.ToBytes(e.TileOffsets)); err != nil {
		return err
	}
	if err := r.storage(&r.tileNode, wgpu.ToBytes(e.TileNodes)); err != nil {
		return err
	}
	if err := r.storage(&r.tileSegOf, wgpu.ToBytes(e.TileSegOff)); err != nil {
		return err
	}
	if err := r.storage(&r.tileSegIx, wgpu.ToBytes(e.TileSegIdx)); err != nil {
		return err
	}
	if err := r.storage(&r.clipsBuf, wgpu.ToBytes(e.Clips)); err != nil {
		return err
	}
	return nil
}

const minBufSize = 32

func (r *Renderer) storage(buf **wgpu.Buffer, data []byte) error {
	need := uint64(len(data))
	if *buf == nil || (*buf).GetSize() < need {
		if *buf != nil {
			(*buf).Release()
			*buf = nil
		}
		size := need + need/2
		if size < minBufSize {
			size = minBufSize
		}
		size = (size + 3) &^ 3
		b, err := r.dev.device.CreateBuffer(&wgpu.BufferDescriptor{
			Size:  size,
			Usage: wgpu.BufferUsageStorage | wgpu.BufferUsageCopySrc | wgpu.BufferUsageCopyDst,
		})
		if err != nil {
			return err
		}
		*buf = b
	}
	if len(data) > 0 {
		return r.dev.queue.WriteBuffer(*buf, 0, data)
	}
	return nil
}

func (r *Renderer) releaseBuffers() {
	for _, b := range []**wgpu.Buffer{&r.segBuf, &r.nodeBuf, &r.stopBuf, &r.tileOff, &r.tileNode, &r.tileSegOf, &r.tileSegIx, &r.clipsBuf} {
		if *b != nil {
			(*b).Release()
			*b = nil
		}
	}
}

func (r *Renderer) Release() {
	r.releaseBuffers()
	if r.ras != nil {
		r.ras.release()
		r.ras = nil
	}
	if r.target != nil {
		r.target.release()
		r.target = nil
	}
	if r.dev != nil {
		r.dev.Release()
		r.dev = nil
	}
}
