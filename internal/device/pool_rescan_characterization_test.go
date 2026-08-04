package device

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 锁住现状:扫描到的硬件复用了某 IMEI 配置的旧路径,但实时 IMEI 与配置不符 → 不得绑定。
func TestRescanCharacterization_MismatchedIMEIOnReusedPathDoesNotBind(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := "devices:\n- id: dev1\n  device_backend: qmi\n  modem_imei: \"111111111111111\"\n  control_device: /dev/cdc-wdm0\n  interface: wwan0\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	origDiscover := discoverQMIDevicesFn
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		return []QMIDevice{{ControlPath: "/dev/cdc-wdm0", NetInterface: "wwan0", USBPath: "/sys/bus/usb/devices/1-2"}}, nil
	}
	t.Cleanup(func() { discoverQMIDevicesFn = origDiscover })

	origResolve := resolveDiscoveredQMIDeviceFn
	resolveDiscoveredQMIDeviceFn = func(dev QMIDevice, timeout time.Duration, allowIMEIProbe bool) (QMIDevice, string) {
		// 返回一个不同的 IMEI
		return dev, "222222222222222"
	}
	t.Cleanup(func() { resolveDiscoveredQMIDeviceFn = origResolve })

	p := NewPool(&config.Config{})
	defer p.cancel()

	err := p.RescanAndReconnect()
	if err != nil {
		t.Fatalf("RescanAndReconnect failed: %v", err)
	}

	w := p.GetWorker("dev1")
	if w != nil {
		t.Fatalf("Expected no worker to be bound due to mismatched IMEI on reused path, got worker: %+v", w.Config)
	}
}
