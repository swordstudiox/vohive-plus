package device

import (
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// deviceIMEIBackfillNeeded 判断是否需要把运行时学到的 IMEI 回填进配置。
// 仅当运行时已学到非空 IMEI、且与配置记录不同(含配置侧为空)时才回填。
// 空 IMEI 绝不触发,确保永不擦除配置里已有的身份。
func deviceIMEIBackfillNeeded(stored, current config.DeviceConfig) bool {
	if strings.TrimSpace(current.ModemIMEI) == "" {
		return false
	}
	return config.NormalizeIMEI(stored.ModemIMEI) != config.NormalizeIMEI(current.ModemIMEI)
}

// persistDeviceAttachmentsIfChanged 设备启动/恢复完成后,只把运行时学到的 IMEI 回填进配置文件,
// 完成一次性身份锚定。绝不写回 control_device / interface / at_port / qmi_device / usb_path /
// audio_device 等易变路径——这些只活在内存,每次按 IMEI 现解析(见 spec 第 5 节)。
// 失败只记日志,不影响设备已成功启动这一事实。
func (p *Pool) persistDeviceAttachmentsIfChanged(cfg config.DeviceConfig) {
	if p == nil || strings.TrimSpace(cfg.ID) == "" {
		return
	}
	stored, err := config.GetDeviceByID(cfg.ID)
	if err != nil || stored == nil {
		return
	}
	if !deviceIMEIBackfillNeeded(*stored, cfg) {
		return
	}
	path := config.GetConfigPath()
	if strings.TrimSpace(path) == "" {
		return
	}
	imei := strings.TrimSpace(cfg.ModemIMEI)
	if err := config.UpdateDeviceIMEIInFile(path, map[string]string{cfg.ID: imei}); err != nil {
		logger.Warn("回填设备 IMEI 到配置文件失败", "device", cfg.ID, "err", err)
		return
	}
	if err := config.ReloadFromFile(); err != nil {
		logger.Warn("回填 IMEI 后重新加载配置文件失败", "device", cfg.ID, "err", err)
		return
	}
	logger.Info("已回填设备 IMEI 到配置文件", "device", cfg.ID, "imei", imei)
}
