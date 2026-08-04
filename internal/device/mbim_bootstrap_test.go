package device

import (
	"context"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestRequiresMBIMCore(t *testing.T) {
	cfg := config.DeviceConfig{DeviceBackend: "mbim", ControlDevice: "/dev/cdc-wdm0"}
	if !requiresMBIMCore(cfg) {
		t.Fatal("mbim mode should require MBIM core")
	}
	if requiresQMICore(cfg) {
		t.Fatal("mbim mode should not require QMI core")
	}
}

func TestNeedsATPortDiscovery(t *testing.T) {
	// MBIM 设备靠 control_device 起,绝不能去反查 AT 端口(它没有 AT 口)。
	mbim := config.DeviceConfig{DeviceBackend: "mbim", ControlDevice: "/dev/cdc-wdm1", ModemIMEI: "359075067694975"}
	if needsATPortDiscovery(mbim) {
		t.Fatal("MBIM device must not trigger AT-port discovery")
	}
	// AT 后端、还没 AT 端口 → 需要按 IMEI 反查。
	at := config.DeviceConfig{DeviceBackend: "at", ModemIMEI: "123456789012345"}
	if !needsATPortDiscovery(at) {
		t.Fatal("AT device without at_port should trigger discovery")
	}
	// AT 后端、已有 AT 端口 → 不需要反查。
	atReady := config.DeviceConfig{DeviceBackend: "at", ATPort: "/dev/ttyUSB2"}
	if needsATPortDiscovery(atReady) {
		t.Fatal("AT device with at_port should not trigger discovery")
	}
}

func TestResolvedBackendModeMBIM(t *testing.T) {
	cfg := config.DeviceConfig{DeviceBackend: "mbim"}
	if got := resolvedBackendMode(cfg); got != backend.BackendMBIM {
		t.Fatalf("resolvedBackendMode = %q, want mbim", got)
	}
}

func TestDeriveESIMTransportMBIM(t *testing.T) {
	cfg := config.DeviceConfig{DeviceBackend: "mbim"}
	if got := deriveESIMTransport(cfg); got != config.ESIMTransportMBIM {
		t.Fatalf("deriveESIMTransport(mbim) = %q, want %q", got, config.ESIMTransportMBIM)
	}

	explicitCfg := config.DeviceConfig{ESIMTransport: "mbim"}
	if got := deriveESIMTransport(explicitCfg); got != config.ESIMTransportMBIM {
		t.Fatalf("deriveESIMTransport(explicit mbim) = %q, want %q", got, config.ESIMTransportMBIM)
	}
}

func TestResolveESIMTransportMBIMDowngradesWhenUICCUnavailable(t *testing.T) {
	cfg := config.DeviceConfig{DeviceBackend: "mbim"}
	if got := resolveESIMTransport(cfg, true); got != config.ESIMTransportMBIM {
		t.Fatalf("resolveESIMTransport(mbim, available) = %q, want %q", got, config.ESIMTransportMBIM)
	}
	if got := resolveESIMTransport(cfg, false); got != config.ESIMTransportAT {
		t.Fatalf("resolveESIMTransport(mbim, unavailable) = %q, want %q", got, config.ESIMTransportAT)
	}

	qmiCfg := config.DeviceConfig{DeviceBackend: "qmi"}
	if got := resolveESIMTransport(qmiCfg, false); got != config.ESIMTransportQMI {
		t.Fatalf("resolveESIMTransport(qmi) = %q, want %q (must not downgrade)", got, config.ESIMTransportQMI)
	}
}

func TestRequiresMBIMCoreWhenExplicitESIMTransportIsMBIM(t *testing.T) {
	if !requiresMBIMCore(config.DeviceConfig{ESIMTransport: "mbim"}) {
		t.Fatal("requiresMBIMCore(explicit mbim) = false, want true")
	}
}

func TestBuildESIMMBIMTransportGate(t *testing.T) {
	sup := &probeStubManager{supported: true}
	tr := buildESIMMBIMTransport(sup)
	if tr == nil {
		t.Fatal("UICC support should return eSIM transport")
	}
	unsup := &probeStubManager{supported: false}
	if buildESIMMBIMTransport(unsup) != nil {
		t.Fatal("unsupported UICC should return nil transport")
	}
}

type probeStubManager struct{ supported bool }

func (p *probeStubManager) ControlDevice() string { return "/dev/cdc-wdm0" }
func (p *probeStubManager) OpenChannel(context.Context, []byte) (uint32, error) {
	return 1, nil
}
func (p *probeStubManager) CloseChannel(context.Context, uint32) error { return nil }
func (p *probeStubManager) TransmitAPDU(context.Context, uint32, []byte) ([]byte, error) {
	return nil, nil
}
func (p *probeStubManager) ProbeUICCSupport(context.Context) bool { return p.supported }
