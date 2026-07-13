package gpu

import (
	"image"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

var _ render.Renderer = (*Renderer)(nil)

type Renderer struct {
	Background wgpu.Color

	dev    *Device
	w, h   int
	target *target

	segBuf  *wgpu.Buffer
	nodeBuf *wgpu.Buffer
	stopBuf *wgpu.Buffer
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
	return &Renderer{
		Background: wgpu.Color{R: 0, G: 0, B: 0, A: 0},
		dev:        dev,
		w:          w,
		h:          h,
		target:     t,
	}, nil
}

func (r *Renderer) Device() *Device { return r.dev }

func (r *Renderer) Render(s *scene.Scene) error {
	enc := Encode(s, r.w, r.h)
	if err := r.upload(enc); err != nil {
		return err
	}
	return r.target.clear(r.dev, r.Background)
}

func (r *Renderer) ReadRGBA() (*image.RGBA, error) { return r.target.readRGBA(r.dev) }

func (r *Renderer) upload(e *Encoded) error {
	r.releaseBuffers()
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
		return nil, nil
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
	if r.target != nil {
		r.target.release()
		r.target = nil
	}
	if r.dev != nil {
		r.dev.Release()
		r.dev = nil
	}
}
