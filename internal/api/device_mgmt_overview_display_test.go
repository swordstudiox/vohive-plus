package api

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 重构后策略字段(network/vowifi/airplane/ip/apn/sms)只存在于运行时投影(w.Config)，
// 不再来自 config.yaml(persisted)。overviewDisplayConfig 必须从 runtime 取这些字段，
// 否则概览 UI 会对所有设备显示 off。
func TestOverviewDisplayConfigTakesPolicyFromRuntime(t *testing.T) {
	runtime := config.DeviceConfig{
		ID:              "wwan0",
		Interface:       "wwan0",
		NetworkEnabled:  true,
		VoWiFiEnabled:   true,
		AirplaneEnabled: true,
		IPVersion:       "v4v6",
		APN:             "ims",
		SMSEnabled:      true,
	}
	persisted := config.DeviceConfig{
		ID:        "wwan0",
		Interface: "", // 持久化里硬件路径为空，应被 runtime 覆盖
		// 策略字段全为零值（重构后 yaml 不再加载）
	}

	got := overviewDisplayConfig(runtime, persisted, true)

	if !got.NetworkEnabled || !got.VoWiFiEnabled || !got.AirplaneEnabled {
		t.Fatalf("策略开关应取自 runtime: %+v", got)
	}
	if got.IPVersion != "v4v6" || got.APN != "ims" {
		t.Fatalf("ip/apn 应取自 runtime: %+v", got)
	}
	if got.Interface != "wwan0" {
		t.Fatalf("硬件路径仍应取自 runtime: %+v", got)
	}
}

// 回归：SMS 是系统不变量（恒开），即使 runtime/persisted 都为 false，
// overviewDisplayConfig 也必须返回 sms_enabled=true，否则短信中心设备会被过滤消失。
func TestOverviewDisplayConfigSMSAlwaysEnabled(t *testing.T) {
	runtime := config.DeviceConfig{ID: "wwan0", Interface: "wwan0", SMSEnabled: false}
	persisted := config.DeviceConfig{ID: "wwan0", SMSEnabled: false}

	got := overviewDisplayConfig(runtime, persisted, true)
	if !got.SMSEnabled {
		t.Fatalf("SMS 应恒为 true（不变量），got=%+v", got)
	}
}
