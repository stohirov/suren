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

// Backend names one native GPU API. The zero value, Any, lets wgpu choose —
// which is what a caller gets from NewDevice and what production uses.
type Backend int

const (
	Any Backend = iota
	Metal
	Vulkan
	DX12
)

func (b Backend) String() string {
	switch b {
	case Any:
		return "any"
	case Metal:
		return "metal"
	case Vulkan:
		return "vulkan"
	case DX12:
		return "dx12"
	}
	return fmt.Sprintf("Backend(%d)", int(b))
}

// Selectable is the set a caller can ask for by name — the portability claim's
// axis (Phase 12d). Any is excluded: it is a request, not a target.
func Selectable() []Backend { return []Backend{Metal, Vulkan, DX12} }

// mask is what wgpu-native actually honours. RequestAdapterOptions.BackendType
// looks like the obvious way to pick a backend and is a trap: wgpu-native warns
// that it is unsupported and hands back whatever adapter it has, so asking for
// Vulkan on a Metal-only host silently returns Metal. A harness built on it
// would report a portability result it never tested. The instance-level mask
// filters for real — an unavailable backend fails to produce an adapter.
func (b Backend) mask() wgpu.InstanceBackend {
	switch b {
	case Metal:
		return wgpu.InstanceBackendMetal
	case Vulkan:
		return wgpu.InstanceBackendVulkan
	case DX12:
		return wgpu.InstanceBackendDX12
	}
	return wgpu.InstanceBackendAll
}

func (b Backend) wants() (wgpu.BackendType, bool) {
	switch b {
	case Metal:
		return wgpu.BackendTypeMetal, true
	case Vulkan:
		return wgpu.BackendTypeVulkan, true
	case DX12:
		return wgpu.BackendTypeD3D12, true
	}
	return wgpu.BackendTypeUndefined, false
}

func NewDevice() (*Device, error) { return NewDeviceOn(Any) }

// NewDeviceOn acquires a device on a specific backend, or fails if the host does
// not expose it. Callers treat that failure as "skip", never as "fail" — the
// point of the portability harness is to test what a host has, not to demand
// hardware of it.
func NewDeviceOn(b Backend) (*Device, error) {
	var desc *wgpu.InstanceDescriptor
	if b != Any {
		desc = &wgpu.InstanceDescriptor{Backends: b.mask()}
	}
	inst := wgpu.CreateInstance(desc)
	if inst == nil {
		return nil, fmt.Errorf("gpu: create instance failed")
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference: wgpu.PowerPreferenceHighPerformance,
	})
	if err != nil {
		inst.Release()
		return nil, fmt.Errorf("gpu: request adapter for %v: %w", b, err)
	}
	// Defence in depth, not a live check: the instance mask above already makes
	// RequestAdapter fail for a backend the host lacks, so this never fires
	// today. It is here because the OTHER selection API in this library accepts
	// the request and quietly ignores it (see mask), and the cost of learning
	// that a second time — a portability result naming a backend that never ran —
	// is far higher than one comparison per device. A claim about which backend
	// ran is worth only what the adapter itself reports.
	if want, ok := b.wants(); ok {
		if got := adapter.GetInfo().BackendType; got != want {
			adapter.Release()
			inst.Release()
			return nil, fmt.Errorf("gpu: asked for %v, adapter reports %v", b, got)
		}
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

// Describe names the adapter a result was actually produced on. A parity claim
// that does not record which driver it ran against is a claim about one laptop.
func (d *Device) Describe() string {
	i := d.adapter.GetInfo()
	return fmt.Sprintf("%v/%v %s", i.BackendType, i.AdapterType, i.DriverDescription)
}

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
