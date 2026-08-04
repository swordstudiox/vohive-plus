package device

import (
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

// deviceIdentityState 存储设备/SIM卡相对静态的身份标识信息
type deviceIdentityState struct {
	IMEI         string                   // 设备的 IMEI 串号
	ICCID        string                   // SIM 卡的唯一识别码 ICCID
	IMSI         string                   // 国际移动用户识别码 IMSI
	NativeSPN    string                   // SIM 卡中内置的原始服务提供商名称
	NativeMCC    string                   // SIM 归属移动国家代码 MCC（由 IMSI + EF-AD 解析）
	NativeMNC    string                   // SIM 归属移动网络代码 MNC（由 IMSI + EF-AD 解析）
	GID1         string                   // 组标识符 1，用于卡片分类与特征匹配
	GID2         string                   // 组标识符 2，用于卡片分类与特征匹配
	PNN          []backend.PNNRecord      // PLMN 网络名称列表记录
	OPL          []backend.OPLRecord      // 运营商 PLMN 关联列表记录
	ServiceTable *backend.SIMServiceTable // SIM 卡内使能的服务功能表
	Ready        bool                     // 标识卡片身份信息是否已读取并就绪
	Phase        string                   // SIM 身份生命周期：ready / transitioning / degraded
	TargetICCID  string                   // 切卡期间期望收敛到的目标 ICCID
	Generation   uint64                   // 身份生命周期代数，用于拒绝旧切卡任务
	LastReason   string                   // 最近一次身份生命周期变化原因
	LastError    string                   // 最近一次身份收敛错误
}

const (
	simIdentityPhaseReady         = "ready"
	simIdentityPhaseTransitioning = "transitioning"
	simIdentityPhaseDegraded      = "degraded"
)

// deviceRuntimeState 存储设备运行时的动态网络状态与射频指标
type deviceRuntimeState struct {
	Firmware       string // 设备当前运行的固件版本
	Operator       string // 当前驻留的基站运营商名称
	NetworkMode    string // 驻网技术模式 (如 LTE, NR5G 等)
	NetworkDuplex  string // 网络双工模式 (如 FDD, TDD)
	RadioBand      string // 当前工作的射频频段 (Band)
	RadioChannel   uint32 // 工作频点 (ARFCN/EARFCN/NR-ARFCN)
	RegStatus      int    // 网络注册状态码 (0:未注册, 1:已注册归属地网络, 5:漫游等)
	RegStatusText  string // 网络注册状态的可读文本描述
	PSAttached     bool   // 分组域 (PS) 附着状态
	SignalDBM      int    // 信号强度指示 (dBm)
	SignalRSRP     int    // 4G/5G 物理层接收信号参考功率
	SignalRSRQ     int    // 4G/5G 接收信号参考质量
	SignalSINR     int    // 4G/5G 信号与干扰加噪声比
	NR5GSignalSINR int    // 5G (NR) 专属信号信噪比
	LAC            string // 位置区代码 (Location Area Code)
	CellID         string // 基站小区 ID
	APN            string // 拨号使用的 APN
	IMSStatus      int    // IMS 服务注册状态
	SimInserted    bool   // SIM 卡是否在位/已插入
	USBNetMode     int    // USB 网卡工作模式
	OperatingMode  *int   // 当前功能运行模式 (如 CFUN 状态值)
	Ready          bool   // 标识运行时状态快照是否已初始化并就绪
}

// deviceStateMeta 描述设备状态数据的元信息（包括更新时间与健康状态）
type deviceStateMeta struct {
	Healthy           bool      // 设备当前是否判定为健康
	UpdatedAt         time.Time // 上次任意状态发生变更的更新时间
	IdentityUpdatedAt time.Time // 上次身份识别数据被刷新的时间
	RuntimeUpdatedAt  time.Time // 上次运行时动态状态被刷新的时间
}

// deviceStateStore 统一聚合设备的身份和运行时状态存储
type deviceStateStore struct {
	Identity deviceIdentityState // 静态身份状态
	Runtime  deviceRuntimeState  // 动态运行状态
	Meta     deviceStateMeta     // 状态对应的元数据
}

// hasRuntimeSnapshot 快速校验给定的设备状态更新对象是否包含任何有效且有意义的运行时属性
func hasRuntimeSnapshot(status modem.DeviceStatus) bool {
	return strings.TrimSpace(status.IMEI) != "" ||
		strings.TrimSpace(status.Firmware) != "" ||
		strings.TrimSpace(status.Operator) != "" ||
		strings.TrimSpace(status.NetworkMode) != "" ||
		strings.TrimSpace(status.RegStatusText) != "" ||
		strings.TrimSpace(status.RadioBand) != "" ||
		strings.TrimSpace(status.LAC) != "" ||
		strings.TrimSpace(status.CellID) != "" ||
		strings.TrimSpace(status.APN) != "" ||
		status.SignalDBM != 0 ||
		status.SignalRSRP != 0 ||
		status.SignalRSRQ != 0 ||
		status.SignalSINR != 0 ||
		status.NR5GSignalSINR != 0 ||
		status.RadioChannel != 0 ||
		status.RegStatus != 0 ||
		status.PSAttached ||
		status.IMSStatus != 0 ||
		status.USBNetMode != 0 ||
		status.OperatingMode != nil ||
		status.SimInserted
}

// projectDeviceStatusLocked 将内部缓存的 Worker 状态数据投影并合并为公开暴露的通用 `modem.DeviceStatus` 结构体
func (w *Worker) projectDeviceStatusLocked() modem.DeviceStatus {
	status := modem.DeviceStatus{
		IMEI:            strings.TrimSpace(w.state.Identity.IMEI),
		ICCID:           strings.TrimSpace(w.state.Identity.ICCID),
		IMSI:            strings.TrimSpace(w.state.Identity.IMSI),
		NativeSPN:       strings.TrimSpace(w.state.Identity.NativeSPN),
		NativeMCC:       strings.TrimSpace(w.state.Identity.NativeMCC),
		NativeMNC:       strings.TrimSpace(w.state.Identity.NativeMNC),
		GID1:            strings.TrimSpace(w.state.Identity.GID1),
		GID2:            strings.TrimSpace(w.state.Identity.GID2),
		PNN:             backendPNNRecordsToModem(w.state.Identity.PNN),
		OPL:             backendOPLRecordsToModem(w.state.Identity.OPL),
		SIMServiceTable: backendSIMServiceTableToModem(w.state.Identity.ServiceTable),
		Firmware:        w.state.Runtime.Firmware,
		Operator:        w.state.Runtime.Operator,
		SimInserted:     w.state.Runtime.SimInserted,
		SignalDBM:       w.state.Runtime.SignalDBM,
		SignalRSRP:      w.state.Runtime.SignalRSRP,
		SignalRSRQ:      w.state.Runtime.SignalRSRQ,
		SignalSINR:      w.state.Runtime.SignalSINR,
		NR5GSignalSINR:  w.state.Runtime.NR5GSignalSINR,
		RadioBand:       w.state.Runtime.RadioBand,
		RadioChannel:    w.state.Runtime.RadioChannel,
		RegStatus:       w.state.Runtime.RegStatus,
		RegStatusText:   w.state.Runtime.RegStatusText,
		PSAttached:      w.state.Runtime.PSAttached,
		LAC:             w.state.Runtime.LAC,
		CellID:          w.state.Runtime.CellID,
		APN:             w.state.Runtime.APN,
		IMSStatus:       w.state.Runtime.IMSStatus,
		NetworkMode:     w.state.Runtime.NetworkMode,
		NetworkDuplex:   w.state.Runtime.NetworkDuplex,
		USBNetMode:      w.state.Runtime.USBNetMode,
		OperatingMode:   w.state.Runtime.OperatingMode,
	}
	return status
}

// ProjectDeviceStatus 线程安全地对外读取并生成指定 Worker 的当前完整设备状态投影
func (w *Worker) ProjectDeviceStatus() modem.DeviceStatus {
	if w == nil {
		return modem.DeviceStatus{}
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return w.projectDeviceStatusLocked()
}

func (w *Worker) BeginSIMIdentityTransition(targetICCID, reason string) uint64 {
	if w == nil {
		return 0
	}
	now := time.Now()
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.beginSIMIdentityTransitionLocked(targetICCID, reason, now)
	return w.state.Identity.Generation
}

func (w *Worker) EnsureSIMIdentityTransition(targetICCID, reason string) uint64 {
	if w == nil {
		return 0
	}
	now := time.Now()
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	target := normalizeSIMIdentity(targetICCID)
	currentTarget := normalizeSIMIdentity(w.state.Identity.TargetICCID)
	if (w.state.Identity.Phase == simIdentityPhaseTransitioning || w.state.Identity.Phase == simIdentityPhaseDegraded) &&
		currentTarget == target {
		w.state.Identity.LastReason = strings.TrimSpace(reason)
		w.state.Identity.LastError = ""
		w.state.Meta.IdentityUpdatedAt = now
		w.state.Meta.UpdatedAt = now
		return w.state.Identity.Generation
	}
	w.beginSIMIdentityTransitionLocked(target, reason, now)
	return w.state.Identity.Generation
}

func (w *Worker) beginSIMIdentityTransitionLocked(targetICCID, reason string, now time.Time) {
	w.state.Identity.ICCID = ""
	w.state.Identity.IMSI = ""
	w.state.Identity.NativeSPN = ""
	w.clearSIMMetadataLocked()
	w.state.Identity.Ready = false
	w.state.Identity.Phase = simIdentityPhaseTransitioning
	w.state.Identity.TargetICCID = normalizeSIMIdentity(targetICCID)
	w.state.Identity.Generation++
	w.state.Identity.LastReason = strings.TrimSpace(reason)
	w.state.Identity.LastError = ""
	w.state.Meta.IdentityUpdatedAt = now
	w.state.Meta.UpdatedAt = now
}

func (w *Worker) MarkSIMIdentityDegraded(reason string, err error) {
	if w == nil {
		return
	}
	now := time.Now()
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.state.Identity.Ready = false
	w.state.Identity.Phase = simIdentityPhaseDegraded
	w.state.Identity.LastReason = strings.TrimSpace(reason)
	if err != nil {
		w.state.Identity.LastError = err.Error()
	} else {
		w.state.Identity.LastError = ""
	}
	w.state.Meta.IdentityUpdatedAt = now
	w.state.Meta.UpdatedAt = now
}

func (w *Worker) SIMIdentityAllowsOverviewFallback() bool {
	if w == nil {
		return false
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	switch w.state.Identity.Phase {
	case simIdentityPhaseTransitioning, simIdentityPhaseDegraded:
		return false
	default:
		return w.state.Identity.Ready && strings.TrimSpace(w.state.Identity.IMSI) != ""
	}
}

func (w *Worker) SIMIdentitySuppressesOverviewIMSI() bool {
	if w == nil {
		return false
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return w.state.Identity.Phase == simIdentityPhaseTransitioning ||
		w.state.Identity.Phase == simIdentityPhaseDegraded
}

func (w *Worker) SIMIdentityConvergenceMatches(targetICCID string, generation uint64) bool {
	if w == nil {
		return false
	}
	target := normalizeSIMIdentityForCompare(targetICCID)
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	if generation != 0 && w.state.Identity.Generation != generation {
		return false
	}
	phase := w.state.Identity.Phase
	currentTarget := normalizeSIMIdentityForCompare(w.state.Identity.TargetICCID)
	if phase == simIdentityPhaseReady {
		return false
	}
	if currentTarget != "" || target != "" {
		return currentTarget == target
	}
	return phase == simIdentityPhaseTransitioning || phase == simIdentityPhaseDegraded || !w.state.Identity.Ready
}

// backendPNNRecordsToModem 将后端的 PNN 记录列表转换为 modem 包中公开的数据结构形式
func backendPNNRecordsToModem(records []backend.PNNRecord) []modem.PNNRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]modem.PNNRecord, 0, len(records))
	for _, record := range records {
		out = append(out, modem.PNNRecord(record))
	}
	return out
}

// backendOPLRecordsToModem 将后端的 OPL 记录列表转换为 modem 包中公开的数据结构形式
func backendOPLRecordsToModem(records []backend.OPLRecord) []modem.OPLRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]modem.OPLRecord, 0, len(records))
	for _, record := range records {
		out = append(out, modem.OPLRecord(record))
	}
	return out
}

// backendSIMServiceTableToModem 将后端的 SIM 卡服务表转换为 modem 包中对应的功能特性表形式
func backendSIMServiceTableToModem(table *backend.SIMServiceTable) *modem.SIMServiceTable {
	if table == nil {
		return nil
	}
	converted := modem.SIMServiceTable(*table)
	return &converted
}

// hasSIMMetadata 判断传入的 SIM 元数据内容是否合法且包含任一有效成员字段
func hasSIMMetadata(meta *backend.SIMMetadata) bool {
	return meta != nil && (strings.TrimSpace(meta.NativeMCC) != "" ||
		strings.TrimSpace(meta.NativeMNC) != "" ||
		strings.TrimSpace(meta.GID1) != "" ||
		strings.TrimSpace(meta.GID2) != "" ||
		len(meta.PNN) > 0 ||
		len(meta.OPL) > 0 ||
		meta.ServiceTable != nil)
}

// mergeSIMMetadataLocked 将读取到的 SIM 元数据安全地合并覆盖至当前设备的身份记录缓存中
func (w *Worker) mergeSIMMetadataLocked(meta *backend.SIMMetadata) bool {
	if !hasSIMMetadata(meta) {
		return false
	}
	w.state.Identity.NativeMCC = strings.TrimSpace(meta.NativeMCC)
	w.state.Identity.NativeMNC = strings.TrimSpace(meta.NativeMNC)
	w.state.Identity.GID1 = strings.TrimSpace(meta.GID1)
	w.state.Identity.GID2 = strings.TrimSpace(meta.GID2)
	w.state.Identity.PNN = append([]backend.PNNRecord(nil), meta.PNN...)
	w.state.Identity.OPL = append([]backend.OPLRecord(nil), meta.OPL...)
	if meta.ServiceTable != nil {
		serviceTable := *meta.ServiceTable
		serviceTable.EnabledServices = append([]int(nil), meta.ServiceTable.EnabledServices...)
		w.state.Identity.ServiceTable = &serviceTable
	} else {
		w.state.Identity.ServiceTable = nil
	}
	return true
}

// clearSIMMetadataLocked 清除静态缓存中的 SIM 卡相关元数据和自定义特征
func (w *Worker) clearSIMMetadataLocked() {
	w.state.Identity.NativeMCC = ""
	w.state.Identity.NativeMNC = ""
	w.state.Identity.GID1 = ""
	w.state.Identity.GID2 = ""
	w.state.Identity.PNN = nil
	w.state.Identity.OPL = nil
	w.state.Identity.ServiceTable = nil
}

// mergeRuntimeStateLocked 将获取的动态设备状态指标安全合并入当前 Worker 的缓存，并打上最新时间戳
func (w *Worker) mergeRuntimeStateLocked(status modem.DeviceStatus, healthy bool) bool {
	if !hasRuntimeSnapshot(status) {
		return false
	}
	if strings.TrimSpace(status.IMEI) != "" {
		w.state.Identity.IMEI = strings.TrimSpace(status.IMEI)
	}
	w.state.Runtime.Firmware = status.Firmware
	w.state.Runtime.Operator = status.Operator
	w.state.Runtime.SimInserted = status.SimInserted
	if status.SignalDBM != 0 {
		w.state.Runtime.SignalDBM = status.SignalDBM
	}
	if status.SignalRSRP != 0 {
		w.state.Runtime.SignalRSRP = status.SignalRSRP
	}
	if status.SignalRSRQ != 0 {
		w.state.Runtime.SignalRSRQ = status.SignalRSRQ
	}
	if status.SignalSINR != 0 {
		w.state.Runtime.SignalSINR = status.SignalSINR
	}
	if status.NR5GSignalSINR != 0 {
		w.state.Runtime.NR5GSignalSINR = status.NR5GSignalSINR
	}
	if strings.TrimSpace(status.RadioBand) != "" {
		w.state.Runtime.RadioBand = status.RadioBand
	}
	if status.RadioChannel != 0 {
		w.state.Runtime.RadioChannel = status.RadioChannel
	}
	if status.RegStatus != 0 || strings.TrimSpace(status.RegStatusText) != "" {
		w.state.Runtime.RegStatus = status.RegStatus
		w.state.Runtime.RegStatusText = status.RegStatusText
		w.state.Runtime.PSAttached = status.PSAttached
	}
	w.state.Runtime.LAC = status.LAC
	w.state.Runtime.CellID = status.CellID
	w.state.Runtime.APN = status.APN
	w.state.Runtime.IMSStatus = status.IMSStatus
	if strings.TrimSpace(status.NetworkMode) != "" {
		w.state.Runtime.NetworkMode = status.NetworkMode
	}
	if strings.TrimSpace(status.NetworkDuplex) != "" {
		w.state.Runtime.NetworkDuplex = status.NetworkDuplex
	}
	w.state.Runtime.USBNetMode = status.USBNetMode
	w.state.Runtime.OperatingMode = status.OperatingMode
	w.state.Runtime.Ready = true
	now := time.Now()
	w.state.Meta.Healthy = healthy
	w.state.Meta.RuntimeUpdatedAt = now
	w.state.Meta.UpdatedAt = now
	return true
}

// CurrentICCID 取当前有效的 ICCID，兼容切换态
func (w *Worker) CurrentICCID() string {
	if w == nil {
		return ""
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	if w.state.Identity.TargetICCID != "" {
		return w.state.Identity.TargetICCID
	}
	return w.state.Identity.ICCID
}
