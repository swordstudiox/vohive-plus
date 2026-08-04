package device

import (
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// MatchedPair 表示一台已配置设备与一块物理硬件按 IMEI 身份配对的结果。
type MatchedPair struct {
	Config   config.DeviceConfig
	Hardware CompatibleModem
	// BackfillIMEI 仅在该配置原本没有 IMEI、靠迁移期弱路径匹配认回时给出,
	// 表示应回填进配置的实时 IMEI。配置本就有 IMEI 时为空。
	BackfillIMEI string
}

// ResolveResult 是身份解析的产物。
type ResolveResult struct {
	Matched   []MatchedPair         // 配置 ↔ 硬件,按 IMEI 唯一配对
	Unmatched []CompatibleModem     // 没对上任何配置的硬件 → 可添加
	Offline   []config.DeviceConfig // 配置存在但当前无对应硬件 → 离线
	Degraded  []CompatibleModem     // 探不到 IMEI 的硬件,无法确立身份
}

// ResolveDeviceIdentities 是 bootstrap / 发现 / 重连 / rescan 的唯一身份判定来源。
// 主配对规则:按归一化 IMEI 配对,路径不再是身份。仅当配置缺 IMEI(老配置)时,
// 才允许按稳定 USB 路径做一次性迁移匹配,并通过 BackfillIMEI 回填身份。
func ResolveDeviceIdentities(hardware []CompatibleModem, configured []config.DeviceConfig) ResolveResult {
	var result ResolveResult

	// 同一颗模组(同 IMEI)可能同时暴露多个组态,先去重为单一主绑定。
	hardware = dedupeCompositions(hardware)

	configByIMEI := map[string]config.DeviceConfig{}
	var legacyConfigs []config.DeviceConfig
	for _, cfg := range configured {
		if key := config.NormalizeIMEI(cfg.ModemIMEI); key != "" {
			if _, ok := configByIMEI[key]; !ok {
				configByIMEI[key] = cfg
			}
			continue
		}
		legacyConfigs = append(legacyConfigs, cfg)
	}

	usedConfigID := map[string]bool{}
	var pendingUnmatched []CompatibleModem // 有 IMEI 但没命中任何 IMEI 配置
	var pendingImeiless []CompatibleModem  // 探不到 IMEI

	// 第一轮:按 IMEI 身份配对(路径无关)。
	for _, hw := range hardware {
		key := config.NormalizeIMEI(hw.IMEI)
		if key == "" {
			pendingImeiless = append(pendingImeiless, hw)
			continue
		}
		if cfg, ok := configByIMEI[key]; ok {
			result.Matched = append(result.Matched, MatchedPair{Config: cfg, Hardware: hw})
			usedConfigID[cfg.ID] = true
			continue
		}
		pendingUnmatched = append(pendingUnmatched, hw)
	}

	// 第二轮:有 IMEI 的硬件按稳定路径迁移到老配置,认回后回填 IMEI。
	for _, hw := range pendingUnmatched {
		if cfg, ok := takeLegacyConfig(legacyConfigs, usedConfigID, hw); ok {
			result.Matched = append(result.Matched, MatchedPair{
				Config:       cfg,
				Hardware:     hw,
				BackfillIMEI: strings.TrimSpace(hw.IMEI),
			})
			continue
		}
		result.Unmatched = append(result.Unmatched, hw)
	}

	// 第三轮:探不到 IMEI 的硬件只能绑老配置(legacy↔legacy,无身份可被冒用),
	// 不回填;绑不上就归 degraded,绝不去碰已有 IMEI 的身份锚定配置。
	for _, hw := range pendingImeiless {
		if cfg, ok := takeLegacyConfig(legacyConfigs, usedConfigID, hw); ok {
			result.Matched = append(result.Matched, MatchedPair{Config: cfg, Hardware: hw})
			continue
		}
		result.Degraded = append(result.Degraded, hw)
	}

	for _, cfg := range configured {
		if !usedConfigID[cfg.ID] {
			result.Offline = append(result.Offline, cfg)
		}
	}

	return result
}

// takeLegacyConfig 在尚未占用的老配置(无 IMEI)中找出与该硬件稳定路径匹配的一个,
// 命中即标记占用并返回。
func takeLegacyConfig(legacyConfigs []config.DeviceConfig, used map[string]bool, hw CompatibleModem) (config.DeviceConfig, bool) {
	for _, cfg := range legacyConfigs {
		if used[cfg.ID] {
			continue
		}
		if legacyPathMatch(cfg, hw) {
			used[cfg.ID] = true
			return cfg, true
		}
	}
	return config.DeviceConfig{}, false
}

// dedupeCompositions 把同一 IMEI 的多块硬件(多组态)合并成单一主绑定,
// 按后端能力择优(qmi > mbim > ncm > ecm > rndis),保持首次出现顺序。
// 无可用 IMEI 的硬件原样保留,各自后续归入 degraded。
func dedupeCompositions(hardware []CompatibleModem) []CompatibleModem {
	primaryIdx := map[string]int{}
	out := make([]CompatibleModem, 0, len(hardware))
	for _, hw := range hardware {
		key := config.NormalizeIMEI(hw.IMEI)
		if key == "" {
			out = append(out, hw)
			continue
		}
		if idx, ok := primaryIdx[key]; ok {
			if compositionRank(hw.Mode) > compositionRank(out[idx].Mode) {
				out[idx] = hw
			}
			continue
		}
		primaryIdx[key] = len(out)
		out = append(out, hw)
	}
	return out
}

func compositionRank(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "qmi":
		return 5
	case "mbim":
		return 4
	case "ncm":
		return 3
	case "ecm":
		return 2
	case "rndis":
		return 1
	default:
		return 0
	}
}

// legacyPathMatch 只用于迁移期:判断一块有实时 IMEI 的硬件是否就是某个无 IMEI 老配置。
// 稳定的 USB 拓扑端口优先且具有否决权——两边都有 USB 路径时必须相等;只有在缺 USB
// 路径无法判定时,才退回到控制节点 / 接口名这类易变键,避免跨物理端口误绑。
func legacyPathMatch(cfg config.DeviceConfig, hw CompatibleModem) bool {
	cfgUSB := strings.TrimSpace(cfg.USBPath)
	hwUSB := strings.TrimSpace(hw.USBPath)
	if cfgUSB != "" && hwUSB != "" {
		return cfgUSB == hwUSB
	}
	if c := strings.TrimSpace(cfg.ControlDevice); c != "" && c == strings.TrimSpace(hw.ControlPath) {
		return true
	}
	if i := strings.TrimSpace(cfg.Interface); i != "" && i == strings.TrimSpace(hw.NetInterface) {
		return true
	}
	return false
}
