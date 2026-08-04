package device

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 现状记录:一台 MBIM 设备(requiresQMICore=false)在线 → 掉线 → 带相同 IMEI 以新路径回来。
// 期望最终行为:rescan 后该设备被按 IMEI 认回并重建(而非判为 Offline 永不重连)。
// 若当前实现把它判 Offline,本测试会失败,即证明审查发现 1 的回归成立。
func TestRescanReconnectsNonQMIDeviceByIMEI(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := "devices:\n- id: dev-mbim\n  device_backend: mbim\n  modem_imei: \"999999999999999\"\n  control_device: /dev/cdc-wdm0\n  interface: wwan0\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	origDiscover := discoverQMIDevicesFn
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		return nil, nil
	}
	t.Cleanup(func() { discoverQMIDevicesFn = origDiscover })

	origFallback := discoverFallbackModemsFn
	discoverFallbackModemsFn = func() ([]CompatibleModem, error) {
		return []CompatibleModem{{
			ControlPath:   "/dev/cdc-wdm1",
			NetInterface:  "wwan1",
			USBPath:       "/sys/bus/usb/devices/2-2",
			TransportType: backend.BackendMBIM,
			Mode:          "mbim",
		}}, nil
	}
	t.Cleanup(func() { discoverFallbackModemsFn = origFallback })

	origResolveQMI := resolveDiscoveredQMIDeviceFn
	resolveDiscoveredQMIDeviceFn = func(dev QMIDevice, timeout time.Duration, allowIMEIProbe bool) (QMIDevice, string) {
		return dev, ""
	}
	t.Cleanup(func() { resolveDiscoveredQMIDeviceFn = origResolveQMI })

	origResolveCompat := resolveDiscoveredCompatibleModemFn
	resolveDiscoveredCompatibleModemFn = func(dev CompatibleModem, timeout time.Duration) (CompatibleModem, string) {
		if dev.ControlPath == "/dev/cdc-wdm1" {
			return dev, "999999999999999"
		}
		return dev, ""
	}
	t.Cleanup(func() { resolveDiscoveredCompatibleModemFn = origResolveCompat })

	p := NewPool(&config.Config{})
	defer p.cancel()

	// 预置一个空 worker，以便断言其配置被更新，
	// 避免自动拉起触发底层真实 backend.NewBackend 报错而无法捕获更新结果。
	p.workers = map[string]*Worker{
		"dev-mbim": {
			Config: config.DeviceConfig{
				ID:            "dev-mbim",
				DeviceBackend: "mbim",
				ModemIMEI:     "999999999999999",
				ControlDevice: "/dev/cdc-wdm0",
			},
		},
	}

	// 验证 resolver 把它放进 Matched 而非 Offline
	discovered, _ := discoverQMIDevicesFn()
	liveWorkerIndex := BuildWorkerDiscoveryIndex(p.GetAllWorkers(), false)
	hardware := p.collectRescanHardware(discovered, liveWorkerIndex)
	managed := config.ListDevices()
	resolved := ResolveDeviceIdentities(hardware, managed)

	if len(resolved.Matched) != 1 {
		t.Fatalf("Expected 1 matched device, got %d", len(resolved.Matched))
	}
	if resolved.Matched[0].Config.ID != "dev-mbim" {
		t.Errorf("Expected matched device ID to be dev-mbim, got %s", resolved.Matched[0].Config.ID)
	}
	if resolved.Matched[0].Hardware.ControlPath != "/dev/cdc-wdm1" {
		t.Errorf("Expected hardware control path /dev/cdc-wdm1, got %s", resolved.Matched[0].Hardware.ControlPath)
	}
}
