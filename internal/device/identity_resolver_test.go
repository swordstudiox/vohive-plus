package device

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 同一颗模组重启后接口名/控制节点/USB 全变,仍必须按 IMEI 认回原配置。
func TestResolveDeviceIdentitiesMatchesByIMEIAcrossChangedPaths(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:            "wwan1",
		ModemIMEI:     "867383058993207",
		Interface:     "wwan0",
		ControlDevice: "/dev/cdc-wdm0",
		USBPath:       "/sys/bus/usb/devices/1-4",
	}}
	hardware := []CompatibleModem{{
		IMEI:         "867383058993207",
		NetInterface: "wwan1",
		ControlPath:  "/dev/cdc-wdm2",
		USBPath:      "/sys/bus/usb/devices/1-9",
		Mode:         "qmi",
	}}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 1 {
		t.Fatalf("Matched = %d, want 1: %+v", len(got.Matched), got)
	}
	pair := got.Matched[0]
	if pair.Config.ID != "wwan1" {
		t.Fatalf("matched config ID = %q, want wwan1", pair.Config.ID)
	}
	if pair.Hardware.ControlPath != "/dev/cdc-wdm2" {
		t.Fatalf("matched hardware control = %q, want /dev/cdc-wdm2", pair.Hardware.ControlPath)
	}
	if pair.BackfillIMEI != "" {
		t.Fatalf("BackfillIMEI = %q, want empty (config already had IMEI)", pair.BackfillIMEI)
	}
	if len(got.Unmatched) != 0 || len(got.Offline) != 0 || len(got.Degraded) != 0 {
		t.Fatalf("want only Matched; got unmatched=%d offline=%d degraded=%d", len(got.Unmatched), len(got.Offline), len(got.Degraded))
	}
}

// 未对上任何配置的有 IMEI 硬件 → 可添加;无对应硬件的配置 → 离线;
// 探不到 IMEI 的硬件 → degraded,绝不按路径误绑。
func TestResolveDeviceIdentitiesClassifiesUnmatchedOfflineDegraded(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:        "wwan1",
		ModemIMEI: "111111111111111",
	}}
	hardware := []CompatibleModem{
		{IMEI: "222222222222222", NetInterface: "wwan0", Mode: "qmi"},                 // 新模组,可添加
		{IMEI: "", NetInterface: "wwan3", ControlPath: "/dev/cdc-wdm9", Mode: "mbim"}, // 探不到 IMEI
	}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 0 {
		t.Fatalf("Matched = %d, want 0: %+v", len(got.Matched), got.Matched)
	}
	if len(got.Unmatched) != 1 || got.Unmatched[0].IMEI != "222222222222222" {
		t.Fatalf("Unmatched = %+v, want single 222...", got.Unmatched)
	}
	if len(got.Offline) != 1 || got.Offline[0].ID != "wwan1" {
		t.Fatalf("Offline = %+v, want [wwan1]", got.Offline)
	}
	if len(got.Degraded) != 1 || got.Degraded[0].ControlPath != "/dev/cdc-wdm9" {
		t.Fatalf("Degraded = %+v, want single cdc-wdm9", got.Degraded)
	}
}

// 老配置没有 IMEI 时,允许按稳定 USB 路径一次性兜底认回,并给出 BackfillIMEI 以回填身份。
func TestResolveDeviceIdentitiesMigratesLegacyConfigByUSBPathAndBackfills(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:        "wwan1",
		ModemIMEI: "", // legacy:从未采到 IMEI
		USBPath:   "/sys/bus/usb/devices/1-4",
		Interface: "wwan1",
	}}
	hardware := []CompatibleModem{{
		IMEI:         "867383058993207",
		USBPath:      "/sys/bus/usb/devices/1-4",
		NetInterface: "wwan0", // 接口名已变,但 USB 端口稳定
		Mode:         "qmi",
	}}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 1 {
		t.Fatalf("Matched = %d, want 1: %+v", len(got.Matched), got)
	}
	pair := got.Matched[0]
	if pair.Config.ID != "wwan1" {
		t.Fatalf("matched config ID = %q, want wwan1", pair.Config.ID)
	}
	if pair.BackfillIMEI != "867383058993207" {
		t.Fatalf("BackfillIMEI = %q, want 867383058993207", pair.BackfillIMEI)
	}
	if len(got.Unmatched) != 0 || len(got.Offline) != 0 || len(got.Degraded) != 0 {
		t.Fatalf("want only Matched; got unmatched=%d offline=%d degraded=%d", len(got.Unmatched), len(got.Offline), len(got.Degraded))
	}
}

// 迁移期弱路径匹配绝不能用易变的接口名跨物理端口误绑:
// 配置在 USB 1-4,硬件在 USB 1-9 只是接口名撞了 → 不得匹配,各自归位。
func TestResolveDeviceIdentitiesLegacyMigrationIgnoresVolatileInterfaceAcrossPorts(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:        "wwan1",
		ModemIMEI: "",
		USBPath:   "/sys/bus/usb/devices/1-4",
		Interface: "wwan1",
	}}
	hardware := []CompatibleModem{{
		IMEI:         "867383058993207",
		USBPath:      "/sys/bus/usb/devices/1-9", // 不同物理端口
		NetInterface: "wwan1",                    // 仅接口名相同
		Mode:         "qmi",
	}}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 0 {
		t.Fatalf("Matched = %+v, want none (different physical port must not bind)", got.Matched)
	}
	if len(got.Unmatched) != 1 {
		t.Fatalf("Unmatched = %+v, want the new module addable on its own", got.Unmatched)
	}
	if len(got.Offline) != 1 || got.Offline[0].ID != "wwan1" {
		t.Fatalf("Offline = %+v, want legacy wwan1 offline", got.Offline)
	}
}

// 同一颗模组同时暴露多个组态(同 IMEI 的 QMI 与 MBIM)应去重为单一绑定,
// 优先级 qmi > mbim;匹配配置时只产生一个 Matched。
func TestResolveDeviceIdentitiesDedupesSameIMEIMultiCompositionForMatched(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:        "wwan1",
		ModemIMEI: "867383058993207",
	}}
	hardware := []CompatibleModem{
		{IMEI: "867383058993207", ControlPath: "/dev/cdc-wdm1", Mode: "mbim", USBPath: "/sys/bus/usb/devices/1-4"},
		{IMEI: "867383058993207", ControlPath: "/dev/cdc-wdm2", Mode: "qmi", USBPath: "/sys/bus/usb/devices/1-9"},
	}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 1 {
		t.Fatalf("Matched = %d, want 1 (deduped): %+v", len(got.Matched), got.Matched)
	}
	if got.Matched[0].Hardware.Mode != "qmi" {
		t.Fatalf("matched composition Mode = %q, want qmi (higher priority)", got.Matched[0].Hardware.Mode)
	}
	if len(got.Unmatched) != 0 || len(got.Degraded) != 0 || len(got.Offline) != 0 {
		t.Fatalf("want only one Matched; got unmatched=%d degraded=%d offline=%d", len(got.Unmatched), len(got.Degraded), len(got.Offline))
	}
}

// 同 IMEI 多组态但无配置时,也只产生一个可添加候选(择优 qmi)。
func TestResolveDeviceIdentitiesDedupesSameIMEIMultiCompositionForUnmatched(t *testing.T) {
	hardware := []CompatibleModem{
		{IMEI: "867383058993207", ControlPath: "/dev/cdc-wdm1", Mode: "mbim", USBPath: "/sys/bus/usb/devices/1-4"},
		{IMEI: "867383058993207", ControlPath: "/dev/cdc-wdm2", Mode: "qmi", USBPath: "/sys/bus/usb/devices/1-9"},
	}

	got := ResolveDeviceIdentities(hardware, nil)

	if len(got.Unmatched) != 1 {
		t.Fatalf("Unmatched = %d, want 1 (deduped): %+v", len(got.Unmatched), got.Unmatched)
	}
	if got.Unmatched[0].Mode != "qmi" {
		t.Fatalf("unmatched composition Mode = %q, want qmi", got.Unmatched[0].Mode)
	}
}

// 两颗模组互换了 USB 端口,必须各自按 IMEI 归位,绝不串到对方配置。
func TestResolveDeviceIdentitiesTwoModulesSwapPortsStayBoundByIMEI(t *testing.T) {
	configured := []config.DeviceConfig{
		{ID: "modemA", ModemIMEI: "111111111111111", USBPath: "/sys/bus/usb/devices/1-4"},
		{ID: "modemB", ModemIMEI: "222222222222222", USBPath: "/sys/bus/usb/devices/1-9"},
	}
	// 端口对调:A 现在在 1-9,B 现在在 1-4。
	hardware := []CompatibleModem{
		{IMEI: "111111111111111", USBPath: "/sys/bus/usb/devices/1-9", Mode: "qmi"},
		{IMEI: "222222222222222", USBPath: "/sys/bus/usb/devices/1-4", Mode: "qmi"},
	}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 2 {
		t.Fatalf("Matched = %d, want 2", len(got.Matched))
	}
	for _, p := range got.Matched {
		if p.Config.ModemIMEI != p.Hardware.IMEI {
			t.Fatalf("cross-bind! config %s (IMEI %s) bound to hardware IMEI %s",
				p.Config.ID, p.Config.ModemIMEI, p.Hardware.IMEI)
		}
	}
	if len(got.Unmatched) != 0 || len(got.Offline) != 0 || len(got.Degraded) != 0 {
		t.Fatalf("want clean 2-match; got unmatched=%d offline=%d degraded=%d", len(got.Unmatched), len(got.Offline), len(got.Degraded))
	}
}

// 老配置无 IMEI、硬件也探不到 IMEI 时:仍按路径绑定(legacy↔legacy 安全,无身份可被冒用),
// 但没有 BackfillIMEI(无 IMEI 可回填)。这保留历史 legacy_path 行为。
func TestResolveDeviceIdentitiesLegacyConfigBindsImeilessHardwareByPathWithoutBackfill(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:            "legacy",
		ModemIMEI:     "",
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
		USBPath:       "/sys/bus/usb/devices/1-1",
	}}
	hardware := []CompatibleModem{{
		IMEI:         "", // 探不到
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan0",
		USBPath:      "/sys/bus/usb/devices/1-1",
		Mode:         "qmi",
	}}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 1 || got.Matched[0].Config.ID != "legacy" {
		t.Fatalf("Matched = %+v, want legacy bound by path", got.Matched)
	}
	if got.Matched[0].BackfillIMEI != "" {
		t.Fatalf("BackfillIMEI = %q, want empty (no IMEI to backfill)", got.Matched[0].BackfillIMEI)
	}
	if len(got.Degraded) != 0 || len(got.Offline) != 0 || len(got.Unmatched) != 0 {
		t.Fatalf("want only Matched; got degraded=%d offline=%d unmatched=%d", len(got.Degraded), len(got.Offline), len(got.Unmatched))
	}
}

// 安全护栏:配置已绑定一个 IMEI,而硬件这轮探不到 IMEI(如 MBIM 挂死)→ 绝不按路径绑定,
// 硬件归 degraded,配置归 offline。
func TestResolveDeviceIdentitiesImeiAnchoredConfigNeverBindsImeilessHardware(t *testing.T) {
	configured := []config.DeviceConfig{{
		ID:            "anchored",
		ModemIMEI:     "333333333333333",
		ControlDevice: "/dev/cdc-wdm0",
		USBPath:       "/sys/bus/usb/devices/1-1",
	}}
	hardware := []CompatibleModem{{
		IMEI:        "", // 挂死探不到
		ControlPath: "/dev/cdc-wdm0",
		USBPath:     "/sys/bus/usb/devices/1-1",
		Mode:        "mbim",
	}}

	got := ResolveDeviceIdentities(hardware, configured)

	if len(got.Matched) != 0 {
		t.Fatalf("Matched = %+v, want none (must not bind imeiless hw to identity-anchored config)", got.Matched)
	}
	if len(got.Degraded) != 1 || len(got.Offline) != 1 {
		t.Fatalf("want degraded=1 offline=1; got degraded=%d offline=%d", len(got.Degraded), len(got.Offline))
	}
}
