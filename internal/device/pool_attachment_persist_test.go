package device

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestDeviceIMEIBackfillNeeded(t *testing.T) {
	stored := config.DeviceConfig{ID: "dev1"} // 配置侧无 IMEI
	learned := config.DeviceConfig{ID: "dev1", ModemIMEI: "867383058993207"}

	if !deviceIMEIBackfillNeeded(stored, learned) {
		t.Fatal("learned IMEI for empty-config should need backfill")
	}

	// 运行时探不到 IMEI(空)→ 绝不触发(不擦除)。
	if deviceIMEIBackfillNeeded(config.DeviceConfig{ID: "dev1", ModemIMEI: "123456789012345"}, config.DeviceConfig{ID: "dev1"}) {
		t.Fatal("empty current IMEI must never trigger backfill")
	}

	// 配置已有相同 IMEI → 无需回填。
	same := config.DeviceConfig{ID: "dev1", ModemIMEI: "867383058993207"}
	if deviceIMEIBackfillNeeded(same, same) {
		t.Fatal("identical IMEI should not need backfill")
	}
}

// 核心回归:路径变化绝不再写回 config.yaml。
// 零路径架构:raw 不含路径键(迁移后磁盘状态),persistDeviceAttachmentsIfChanged 也绝不写入路径。
func TestPersistDeviceAttachmentsDoesNotWritePaths(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := "devices:\n- id: dev-qmi\n  device_backend: qmi\n  modem_imei: \"867383058993207\"\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	// Load() 不触发迁移(无路径键),文件与 raw 一致,捕获为基线。
	baseline, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() baseline error = %v", err)
	}

	p := NewPool(&config.Config{})
	defer p.cancel()

	// 运行时解析出全新路径(热插拔后内核重排),且 IMEI 与配置一致。
	newCfg := config.DeviceConfig{
		ID:            "dev-qmi",
		ModemIMEI:     "867383058993207",
		ControlDevice: "/dev/cdc-wdm2",
		Interface:     "wwan1",
		ATPort:        "/dev/ttyUSB6",
	}
	p.persistDeviceAttachmentsIfChanged(newCfg)

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(baseline) {
		t.Fatalf("config.yaml must stay byte-identical when only paths change:\nbefore=%q\nafter=%q", baseline, string(after))
	}
}

// 老配置(无 modem_imei)探到 live IMEI 后,被回填进配置文件。
// 零路径架构:路径字段由迁移从磁盘删除,Load() 也不从文件读取,所以 got 里路径全为空。
func TestPersistDeviceAttachmentsBackfillsLearnedIMEIForLegacyConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := "devices:\n- id: dev-qmi\n  device_backend: qmi\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	p := NewPool(&config.Config{})
	defer p.cancel()

	learned := config.DeviceConfig{
		ID:            "dev-qmi",
		ModemIMEI:     "867383058993207",
		ControlDevice: "/dev/cdc-wdm2", // 运行时路径,绝不写回磁盘
		Interface:     "wwan1",
		ATPort:        "/dev/ttyUSB6",
	}
	p.persistDeviceAttachmentsIfChanged(learned)

	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := updated.Devices[0]
	if got.ModemIMEI != "867383058993207" {
		t.Fatalf("ModemIMEI = %q, want backfilled 867383058993207", got.ModemIMEI)
	}
	// 零路径架构: Load() 绝不从文件回填运行时路径字段(mapstructure:"-")。
	if got.ControlDevice != "" || got.Interface != "" || got.ATPort != "" {
		t.Fatalf("runtime path fields must not be loaded from file, got: %+v", got)
	}
}

// 安全护栏:探不到 IMEI(current 为空)时绝不擦除配置里已有的 IMEI,也不写路径。
// 零路径架构:raw 不含路径键,LoadGlobalManager 不触发迁移,文件保持稳定。
func TestPersistDeviceAttachmentsNeverErasesExistingIMEI(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := "devices:\n- id: dev-qmi\n  device_backend: qmi\n  modem_imei: \"123456789012345\"\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	baseline, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() baseline error = %v", err)
	}

	p := NewPool(&config.Config{})
	defer p.cancel()

	imeiless := config.DeviceConfig{
		ID:            "dev-qmi",
		ModemIMEI:     "",
		ControlDevice: "/dev/cdc-wdm2",
		Interface:     "wwan1",
		ATPort:        "/dev/ttyUSB6",
	}
	p.persistDeviceAttachmentsIfChanged(imeiless)

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(baseline) {
		t.Fatalf("config.yaml must stay byte-identical (no imei erase, no path write):\nbefore=%q\nafter=%q", baseline, string(after))
	}
}
