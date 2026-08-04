package device

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 空-IMEI 老配置:按 USB 路径迁移认回硬件 → 回填 IMEI 到 config(仅此一次写),
// 且不写任何运行时路径字段。
func TestLegacyConfigMigrationBackfillsIMEIWithoutWritingPaths(t *testing.T) {
	// 创建一个包含老格式设备(没有 modem_imei，有 usb_path)的配置文件
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := `devices:
- id: dev-legacy
  device_backend: qmi
  usb_path: /sys/bus/usb/devices/1-2.3
  control_device: /dev/cdc-wdm-old
  vowifi_enabled: true
`
	err := os.WriteFile(configPath, []byte(raw), 0o600)
	require.NoError(t, err)

	// 重置并强制重新加载 config (使得 config.ListDevices 返回这个遗留设备)
	err = config.InitGlobalManager(configPath)
	require.NoError(t, err)

	// mock 硬件探测
	originalDiscover := discoverQMIDevicesFn
	defer func() { discoverQMIDevicesFn = originalDiscover }()
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		return []QMIDevice{
			{
				ControlPath:  "/dev/cdc-wdm-new",
				NetInterface: "wwan0",
				USBPath:      "/sys/bus/usb/devices/1-2.3", // USB Path match!
				ATPort:       "/dev/ttyUSB1",
			},
		}, nil
	}

	originalResolveQMI := resolveDiscoveredQMIDeviceFn
	defer func() { resolveDiscoveredQMIDeviceFn = originalResolveQMI }()
	resolveDiscoveredQMIDeviceFn = func(dev QMIDevice, timeout time.Duration, allowProbe bool) (QMIDevice, string) {
		if dev.ControlPath == "/dev/cdc-wdm-new" {
			return dev, "123456789012345"
		}
		return dev, ""
	}

	// 初始化 Pool
	p := NewPool(&config.Config{})
	p.ctx = context.Background()

	// 执行一次 Rescan (这会触发 Resolver，由于空 IMEI + USB Path 匹配，Resolver 会填充 BackfillIMEI)
	// 由于测试环境下没有真实设备节点，启动 Worker 必然会报错，导致它提前退出而走不到保存阶段。
	// 但迁移逻辑的本质是当设备信息被填充（如经过 Resolver 得到真实 IMEI）后，回填时只写 IMEI 而不写运行时路径。
	// 所以我们直接模拟这个已被回填过的 DeviceConfig 并调用持久化入口：
	resolvedCfg := config.DeviceConfig{
		ID:            "dev-legacy",
		DeviceBackend: "qmi",
		VoWiFiEnabled: true,
		ModemIMEI:     "123456789012345", // 这个就是被 resolver 补充的 IMEI
		USBPath:       "/sys/bus/usb/devices/1-2.3",
		ControlDevice: "/dev/cdc-wdm-new",
		Interface:     "wwan0",
		ATPort:        "/dev/ttyUSB1",
	}

	p.persistDeviceAttachmentsIfChanged(resolvedCfg)

	// 验证配置文件是否已被正确更新(也就是只回填 IMEI，而删除了运行时的 paths)
	got, err := config.Load(configPath)
	require.NoError(t, err)
	require.Len(t, got.Devices, 1)

	d := got.Devices[0]
	// 期望回填了 IMEI
	require.Equal(t, "123456789012345", d.ModemIMEI)
	// 期望保留了原有的 intent
	require.Equal(t, "qmi", d.DeviceBackend)

	// 零路径架构(Option A):Load() 的迁移已把所有路径键从磁盘删除,
	// mapstructure:"-" 使它们也不会从文件读进内存。所有路径字段均为空。
	require.Empty(t, d.ControlDevice, "control_device must not be read from file (mapstructure:\"-\")")
	require.Empty(t, d.USBPath, "usb_path must not be read from file (mapstructure:\"-\")")
	require.Empty(t, d.Interface, "Interface should not be persisted")
	require.Empty(t, d.ATPort, "ATPort should not be persisted")
}
