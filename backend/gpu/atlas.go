package gpu

import (
	"fmt"

	"github.com/cogentcore/webgpu/wgpu"
)

// atlas is the texture behind every image paint in a frame — see Encoded.Atlas
// for the layout and for why it is a texture rather than a ninth storage buffer.
//
// It is bound on EVERY dispatch, including the overwhelming majority of frames
// that contain no image at all, because a bind group must supply every binding
// the layout declares. So a renderer always has one, and a frame with no images
// gets the 1x1 placeholder below rather than a branch in the shader.
type atlas struct {
	tex  *wgpu.Texture
	view *wgpu.TextureView
	w, h int
}

func newAtlas(d *Device, w, h int) (*atlas, error) {
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	if lim := int(d.device.GetLimits().Limits.MaxTextureDimension2D); w > lim || h > lim {
		// Checked here rather than left to CreateTexture, so the error names the
		// thing the caller can act on. This is a backend limit and not a parity
		// break: the CPU reference renders the scene fine, and an error is a
		// different fact from a wrong pixel. A scene that hits it wants fewer or
		// smaller images, or the atlas packer this deliberately is not (see Encoded).
		return nil, fmt.Errorf("gpu: image atlas %dx%d exceeds maxTextureDimension2D %d", w, h, lim)
	}
	tex, err := d.device.CreateTexture(&wgpu.TextureDescriptor{
		Usage:         wgpu.TextureUsageTextureBinding | wgpu.TextureUsageCopyDst,
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
	return &atlas{tex: tex, view: view, w: w, h: h}, nil
}

func (a *atlas) release() {
	if a == nil {
		return
	}
	if a.view != nil {
		a.view.Release()
		a.view = nil
	}
	if a.tex != nil {
		a.tex.Release()
		a.tex = nil
	}
}

// uploadAtlas makes the GPU texture match the encoder's. It re-uploads only when
// the texels changed: Encoded.AtlasFP hashes them alone, so a frame that moves an
// image node without touching its pixels — an animation, the common case — pays
// nothing. (An unchanged WHOLE frame never gets here; Phase 7c's skip fires first.)
//
// A resize resets the retained hash rather than trusting it, because a fresh
// texture's contents are undefined and a matching hash would otherwise skip the
// upload into it.
func (r *Renderer) uploadAtlas(e *Encoded) error {
	w, h := e.AtlasW, e.AtlasH
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	if r.atlas == nil || r.atlas.w != w || r.atlas.h != h {
		a, err := newAtlas(r.dev, w, h)
		if err != nil {
			return err
		}
		r.atlas.release()
		r.atlas = a
		r.lastAtlasFP = 0
		r.haveAtlas = false
	}
	if len(e.Atlas) == 0 {
		return nil
	}
	if r.haveAtlas && r.lastAtlasFP == e.AtlasFP {
		return nil
	}
	if err := r.dev.queue.WriteTexture(
		&wgpu.ImageCopyTexture{Texture: r.atlas.tex},
		e.Atlas,
		&wgpu.TextureDataLayout{BytesPerRow: uint32(e.AtlasW * 4), RowsPerImage: uint32(e.AtlasH)},
		&wgpu.Extent3D{Width: uint32(e.AtlasW), Height: uint32(e.AtlasH), DepthOrArrayLayers: 1},
	); err != nil {
		return err
	}
	r.lastAtlasFP = e.AtlasFP
	r.haveAtlas = true
	return nil
}
