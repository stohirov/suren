package gpu

import (
	_ "embed"
	"unsafe"

	"github.com/cogentcore/webgpu/wgpu"
)

//go:embed raster.wgsl
var rasterWGSL string

type rasterizer struct {
	pipeline *wgpu.ComputePipeline
	layout   *wgpu.BindGroupLayout
	uniform  *wgpu.Buffer
	w, h     int
}

func newRasterizer(d *Device, w, h int) (*rasterizer, error) {
	mod, err := d.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		WGSLDescriptor: &wgpu.ShaderModuleWGSLDescriptor{Code: rasterWGSL},
	})
	if err != nil {
		return nil, err
	}
	defer mod.Release()

	pipeline, err := d.device.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Compute: wgpu.ProgrammableStageDescriptor{Module: mod, EntryPoint: "main"},
	})
	if err != nil {
		return nil, err
	}

	uniform, err := d.device.CreateBuffer(&wgpu.BufferDescriptor{
		Size:  16,
		Usage: wgpu.BufferUsageUniform | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, err
	}

	return &rasterizer{
		pipeline: pipeline,
		layout:   pipeline.GetBindGroupLayout(0),
		uniform:  uniform,
		w:        w,
		h:        h,
	}, nil
}

func (r *rasterizer) run(d *Device, t *target, segs, nodes, tileOff, tileNodes *wgpu.Buffer, nx, ny int) error {
	dims := [4]uint32{uint32(r.w), uint32(r.h), uint32(nx), uint32(ny)}
	if err := d.queue.WriteBuffer(r.uniform, 0, unsafe.Slice((*byte)(unsafe.Pointer(&dims[0])), 16)); err != nil {
		return err
	}

	group, err := d.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: r.layout,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, TextureView: t.view},
			{Binding: 1, Buffer: segs, Size: segs.GetSize()},
			{Binding: 2, Buffer: nodes, Size: nodes.GetSize()},
			{Binding: 3, Buffer: r.uniform, Size: 16},
			{Binding: 4, Buffer: tileOff, Size: tileOff.GetSize()},
			{Binding: 5, Buffer: tileNodes, Size: tileNodes.GetSize()},
		},
	})
	if err != nil {
		return err
	}
	defer group.Release()

	enc, err := d.device.CreateCommandEncoder(nil)
	if err != nil {
		return err
	}
	pass := enc.BeginComputePass(nil)
	pass.SetPipeline(r.pipeline)
	pass.SetBindGroup(0, group, nil)
	pass.DispatchWorkgroups(uint32(nx), (uint32(r.h)+63)/64, 1)
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

func (r *rasterizer) release() {
	if r.uniform != nil {
		r.uniform.Release()
	}
	if r.layout != nil {
		r.layout.Release()
	}
	if r.pipeline != nil {
		r.pipeline.Release()
	}
}
