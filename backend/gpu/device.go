package gpu

import (
	"fmt"

	"github.com/cogentcore/webgpu/wgpu"
)

type Device struct {
	instance *wgpu.Instance
	adapter  *wgpu.Adapter
	device   *wgpu.Device
	queue    *wgpu.Queue
}

func NewDevice() (*Device, error) {
	inst := wgpu.CreateInstance(nil)
	if inst == nil {
		return nil, fmt.Errorf("gpu: create instance failed")
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference: wgpu.PowerPreferenceHighPerformance,
	})
	if err != nil {
		inst.Release()
		return nil, fmt.Errorf("gpu: request adapter: %w", err)
	}
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		adapter.Release()
		inst.Release()
		return nil, fmt.Errorf("gpu: request device: %w", err)
	}
	return &Device{instance: inst, adapter: adapter, device: device, queue: device.GetQueue()}, nil
}

func (d *Device) Info() wgpu.AdapterInfo { return d.adapter.GetInfo() }

func (d *Device) Release() {
	if d.queue != nil {
		d.queue.Release()
	}
	if d.device != nil {
		d.device.Release()
	}
	if d.adapter != nil {
		d.adapter.Release()
	}
	if d.instance != nil {
		d.instance.Release()
	}
	*d = Device{}
}
