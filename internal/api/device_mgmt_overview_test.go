package api

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 零路径持久化后,持久化 config 不再含 control_device/interface 等路径;
// 总览展示必须用 worker 运行时 config 的路径与策略投影,意图字段(name/backend)仍取持久化值。
func TestOverviewDisplayConfigPrefersRuntimePaths(t *testing.T) {
	runtime := config.DeviceConfig{
		ID: "wwan0", ModemIMEI: "863212060398051", DeviceBackend: "qmi",
		Interface: "wwan0", ControlDevice: "/dev/cdc-wdm0", ATPort: "/dev/ttyUSB2",
		QMIDevice: "/dev/cdc-wdm0", USBPath: "/sys/bus/usb/devices/1-4",
		VoWiFiEnabled: true, // 策略跟卡走，存在于运行时投影
	}
	persisted := config.DeviceConfig{
		ID: "wwan0", Name: "主卡", ModemIMEI: "863212060398051", DeviceBackend: "qmi",
	}

	got := overviewDisplayConfig(runtime, persisted, true)

	if got.Name != "主卡" || !got.VoWiFiEnabled || got.DeviceBackend != "qmi" {
		t.Fatalf("intent fields lost: %+v", got)
	}
	if got.ControlDevice != "/dev/cdc-wdm0" || got.Interface != "wwan0" ||
		got.ATPort != "/dev/ttyUSB2" || got.QMIDevice != "/dev/cdc-wdm0" ||
		got.USBPath != "/sys/bus/usb/devices/1-4" {
		t.Fatalf("runtime paths not applied: %+v", got)
	}
}

func TestOverviewDisplayConfigNoPersistedReturnsRuntime(t *testing.T) {
	runtime := config.DeviceConfig{ID: "x", ControlDevice: "/dev/cdc-wdm1", Interface: "wwan1"}
	got := overviewDisplayConfig(runtime, config.DeviceConfig{}, false)
	if got.ControlDevice != "/dev/cdc-wdm1" || got.Interface != "wwan1" {
		t.Fatalf("expected runtime config, got %+v", got)
	}
}
