package api

import (
	"strings"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestValidateDeviceBackendConfigMBIM(t *testing.T) {
	// 零路径架构: control_device 由运行时从 IMEI 发现,不再是保存前置条件。
	// mbim 配置无论是否含 control_device 均合法。
	for _, b := range []string{"mbim", "MBIM"} {
		if err := validateDeviceBackendConfig(config.DeviceConfig{DeviceBackend: b}); err != nil {
			t.Fatalf("backend=%q 应合法，却返回: %v", b, err)
		}
		if err := validateDeviceBackendConfig(config.DeviceConfig{DeviceBackend: b, ControlDevice: "/dev/cdc-wdm2"}); err != nil {
			t.Fatalf("backend=%q+control_device 应合法，却返回: %v", b, err)
		}
	}

	err := validateDeviceBackendConfig(config.DeviceConfig{DeviceBackend: "foo"})
	if err == nil || !strings.Contains(err.Error(), "mbim") {
		t.Fatalf("非法值错误信息应列出 mbim，实际: %v", err)
	}

	for _, b := range []string{"", "at", "qmi"} {
		if err := validateDeviceBackendConfig(config.DeviceConfig{DeviceBackend: b}); err != nil {
			t.Fatalf("backend=%q 应合法，却返回: %v", b, err)
		}
	}
}
