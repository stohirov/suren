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

	segBuf  *wgpu.Buffer
	nodeBuf *wgpu.Buffer
	stopBuf *wgpu.Buffer
	nNodes  int
}

func NewRenderer(w, h int) (*Renderer, error) {
	dev, err := NewDevice()
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
	return &Renderer{dev: dev, w: w, h: h, target: t, ras: ras}, nil
}

func (r *Renderer) Device() *Device { return r.dev }

func (r *Renderer) Render(s *scene.Scene) error {
	e := Encode(s, r.w, r.h)
	if err := r.upload(e); err != nil {
		return err
	}
	return r.ras.run(r.dev, r.target, r.segBuf, r.nodeBuf, r.nNodes)
}

func (r *Renderer) ReadRGBA() (*image.RGBA, error) { return r.target.readRGBA(r.dev) }

func (r *Renderer) upload(e *Encoded) error {
	r.releaseBuffers()
	r.nNodes = len(e.Nodes)
	var err error
	if r.segBuf, err = r.storage(wgpu.ToBytes(e.Segments)); err != nil {
		return err
	}
	if r.nodeBuf, err = r.storage(wgpu.ToBytes(e.Nodes)); err != nil {
		return err
	}
	if r.stopBuf, err = r.storage(wgpu.ToBytes(e.Stops)); err != nil {
		return err
	}
	return nil
}

func (r *Renderer) storage(data []byte) (*wgpu.Buffer, error) {
	if len(data) == 0 {
		return r.dev.device.CreateBuffer(&wgpu.BufferDescriptor{Size: 16, Usage: wgpu.BufferUsageStorage})
	}
	return r.dev.device.CreateBufferInit(&wgpu.BufferInitDescriptor{
		Contents: data,
		Usage:    wgpu.BufferUsageStorage | wgpu.BufferUsageCopySrc,
	})
}

func (r *Renderer) releaseBuffers() {
	for _, b := range []**wgpu.Buffer{&r.segBuf, &r.nodeBuf, &r.stopBuf} {
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
