package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/esim"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

var postSwitchSIMAuthRecoveryDelays = []time.Duration{
	500 * time.Millisecond,
	500 * time.Millisecond,
	500 * time.Millisecond,
	500 * time.Millisecond,
	500 * time.Millisecond,
}

var postSwitchSIMAuthReadyWaitTimeout = 30 * time.Second

const (
	postSwitchSIMAuthUSIMAID = "A0000000871002"
)

type esimSwitchContext struct {
	VoWiFiActiveBefore   bool
	FlightModeBefore     bool
	QMIConnectedBefore   bool
	NetworkEnabledBefore bool
	ICCIDBefore          string
	IMSIBefore           string
	TargetICCID          string
	SwitchToken          uint64
	IdentityGeneration   uint64
	CapturedAt           time.Time
	Phase                esim.SwitchPhase
	PhaseUpdatedAt       time.Time
}

var (
	postSwitchIdentityPollTimeout  = 10 * time.Second
	postSwitchIdentityPollInterval = 500 * time.Millisecond
	postSwitchIdentityRetryDelays  = []time.Duration{time.Second, 3 * time.Second, 6 * time.Second}
)

func (w *Worker) setSwitchEventSource(src *switchEventSource) {
	if w == nil {
		return
	}
	w.switchEvents.Store(src)
}

func (w *Worker) currentSwitchEventSource() *switchEventSource {
	if w == nil {
		return nil
	}
	return w.switchEvents.Load()
}

func isFlightOperatingMode(mode backend.OperatingMode) bool {
	return mode == backend.ModeRFOff || mode == backend.ModeLowPower
}

func (p *Pool) captureESIMSwitchContext(deviceID string, targetICCID string) esimSwitchContext {
	ctx := esimSwitchContext{
		CapturedAt:  time.Now(),
		TargetICCID: normalizeSIMIdentity(targetICCID),
	}
	worker := p.GetWorker(deviceID)
	if worker == nil {
		return ctx
	}

	cached := worker.GetCachedDeviceStatus()
	ctx.ICCIDBefore = strings.TrimSpace(cached.ICCID)
	ctx.IMSIBefore = strings.TrimSpace(cached.IMSI)
	ctx.NetworkEnabledBefore = worker.Config.NetworkEnabled
	if nc := worker.NetworkController(); nc != nil {
		ctx.QMIConnectedBefore = nc.IsConnected()
	}
	ctx.VoWiFiActiveBefore = p.IsVoWiFiActive(deviceID)

	if worker.Backend != nil {
		if opMode, err := worker.Backend.GetOperatingMode(context.Background()); err == nil {
			ctx.FlightModeBefore = isFlightOperatingMode(opMode)
		} else {
			logger.Warn("切卡前读取飞行模式状态失败", "device", deviceID, "err", err)
		}
	}
	return ctx
}

func (p *Pool) beginESIMSwitch(deviceID string, targetICCID string) esimSwitchContext {
	snapshot := p.captureESIMSwitchContext(deviceID, targetICCID)
	p.switchMu.Lock()
	if p.switchingDevices == nil {
		p.switchingDevices = make(map[string]bool)
	}
	if p.switchContexts == nil {
		p.switchContexts = make(map[string]esimSwitchContext)
	}
	if p.switchTokens == nil {
		p.switchTokens = make(map[string]uint64)
	}
	p.switchSeq++
	snapshot.SwitchToken = p.switchSeq
	snapshot.Phase = esim.SwitchPhasePrepare
	snapshot.PhaseUpdatedAt = time.Now()
	p.switchingDevices[deviceID] = true
	p.switchContexts[deviceID] = snapshot
	p.switchTokens[deviceID] = snapshot.SwitchToken
	p.switchMu.Unlock()

	go func(capturedAt time.Time) {
		<-time.After(2 * time.Minute)
		p.switchMu.Lock()
		defer p.switchMu.Unlock()
		current, ok := p.switchContexts[deviceID]
		if !ok || !current.CapturedAt.Equal(capturedAt) {
			return
		}
		delete(p.switchContexts, deviceID)
		delete(p.switchingDevices, deviceID)
		delete(p.switchTokens, deviceID)
		logger.Warn("切卡超时保护触发，已自动清理切卡中标记", "device", deviceID)
	}(snapshot.CapturedAt)

	logger.Info("切卡前已记录运行态快照",
		"device", deviceID,
		"vowifi_before", snapshot.VoWiFiActiveBefore,
		"flight_before", snapshot.FlightModeBefore,
		"qmi_connected_before", snapshot.QMIConnectedBefore,
		"network_enabled_before", snapshot.NetworkEnabledBefore,
		"target_iccid", snapshot.TargetICCID,
		"switch_phase", snapshot.Phase)
	return snapshot
}

func (p *Pool) markESIMSwitchPhase(deviceID string, phase esim.SwitchPhase) {
	p.markESIMSwitchPhaseIfToken(deviceID, 0, phase)
}

func (p *Pool) markESIMSwitchPhaseIfToken(deviceID string, token uint64, phase esim.SwitchPhase) bool {
	deviceID = strings.TrimSpace(deviceID)
	if p == nil || deviceID == "" || phase == "" {
		return false
	}
	now := time.Now()
	var phaseMS int64
	p.switchMu.Lock()
	snapshot, ok := p.switchContexts[deviceID]
	currentToken := p.switchTokens[deviceID]
	if token != 0 && currentToken != token {
		p.switchMu.Unlock()
		logger.Debug("忽略过期 eSIM 切卡阶段更新",
			"device", deviceID,
			"switch_phase", phase,
			"switch_token", token,
			"current_switch_token", currentToken)
		return false
	}
	if ok {
		if !snapshot.PhaseUpdatedAt.IsZero() {
			phaseMS = now.Sub(snapshot.PhaseUpdatedAt).Milliseconds()
		}
		snapshot.Phase = phase
		snapshot.PhaseUpdatedAt = now
		p.switchContexts[deviceID] = snapshot
	}
	logToken := token
	if logToken == 0 {
		logToken = snapshot.SwitchToken
	}
	p.switchMu.Unlock()
	logger.Info("eSIM 切卡阶段更新",
		"device", deviceID,
		"switch_phase", phase,
		"switch_token", logToken,
		"phase_ms", phaseMS,
		"phase_known", ok)
	return ok
}

func (p *Pool) updateESIMSwitchIdentityGeneration(deviceID string, token uint64, generation uint64) {
	deviceID = strings.TrimSpace(deviceID)
	if p == nil || deviceID == "" || generation == 0 {
		return
	}
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	currentToken := p.switchTokens[deviceID]
	if token != 0 && currentToken != token {
		return
	}
	snapshot, ok := p.switchContexts[deviceID]
	if !ok {
		return
	}
	snapshot.IdentityGeneration = generation
	p.switchContexts[deviceID] = snapshot
}

func (p *Pool) isLatestSwitchToken(deviceID string, token uint64) bool {
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	current := p.switchTokens[deviceID]
	return token != 0 && current == token
}

func (p *Pool) finishESIMSwitch(deviceID string) (esimSwitchContext, bool) {
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	snapshot, ok := p.switchContexts[deviceID]
	return snapshot, ok
}

func (p *Pool) clearESIMSwitch(deviceID string) {
	p.switchMu.Lock()
	delete(p.switchContexts, deviceID)
	delete(p.switchingDevices, deviceID)
	delete(p.switchTokens, deviceID)
	p.switchMu.Unlock()
}

func (p *Pool) clearESIMSwitchIfToken(deviceID string, token uint64) {
	if token == 0 {
		return
	}
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	if p.switchTokens[deviceID] != token {
		return
	}
	delete(p.switchContexts, deviceID)
	delete(p.switchingDevices, deviceID)
	delete(p.switchTokens, deviceID)
}

func (p *Pool) applyNetworkPreferenceForSwitchSnapshot(worker *Worker, snapshot esimSwitchContext) error {
	if worker == nil {
		return fmt.Errorf("worker 不存在")
	}
	nc := worker.NetworkController()
	if nc == nil {
		if snapshot.QMIConnectedBefore || snapshot.NetworkEnabledBefore {
			return fmt.Errorf("当前设备缺少数据面能力")
		}
		return nil
	}

	wantConnected := snapshot.QMIConnectedBefore || snapshot.NetworkEnabledBefore
	if wantConnected {
		if nc.IsConnected() {
			return nil
		}
		return worker.StartNetwork()
	}

	if !nc.IsConnected() {
		worker.clearCachedIP()
		return nil
	}
	return worker.StopNetwork()
}

func releaseRadioBeforeSwitch(deviceID string, worker *Worker) {
	if worker == nil || worker.Backend == nil {
		return
	}
	controller, ok := worker.Backend.(backend.OperatingModeController)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := controller.SetOperatingMode(ctx, backend.ModeLowPower); err != nil {
		logger.Warn("eSIM 切卡前释放 radio 失败，继续切卡",
			"device", deviceID,
			"err", err)
		return
	}
	logger.Info("eSIM 切卡前已释放 radio",
		"device", deviceID,
		"mode", backend.ModeLowPower)
}

func (p *Pool) bringRadioOnlineAfterSwitch(deviceID string, worker *Worker, snapshot esimSwitchContext, attachTimeout time.Duration) {
	if worker == nil || worker.Backend == nil {
		return
	}
	controller, ok := worker.Backend.(backend.OperatingModeController)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := controller.SetOperatingMode(ctx, backend.ModeOnline)
	cancel()
	if err != nil {
		logger.Warn("eSIM 切卡后恢复 radio 在线失败，继续后续收敛",
			"device", deviceID,
			"err", err)
		return
	} else {
		logger.Info("eSIM 切卡后已恢复 radio 在线",
			"device", deviceID,
			"mode", backend.ModeOnline)
	}
	if attachTimeout <= 0 {
		return
	}
	if err := p.WaitQMICoreReady(deviceID, attachTimeout); err != nil {
		logger.Warn("eSIM 切卡后等待 NAS attach/身份就绪超时，继续后续收敛",
			"device", deviceID,
			"timeout", attachTimeout.String(),
			"err", err)
		return
	}
	logger.Info("eSIM 切卡后 NAS attach/身份已就绪",
		"device", deviceID,
		"timeout", attachTimeout.String())
}

func (p *Pool) handleESIMSwitchBefore(deviceID string, targetICCID string) uint64 {
	snapshot := p.beginESIMSwitch(deviceID, targetICCID)
	if worker := p.GetWorker(deviceID); worker != nil {
		worker.markHealthRecoveryWindow(qmiHealthGraceAfterSwitch)
		snapshot.IdentityGeneration = worker.BeginSIMIdentityTransition(snapshot.TargetICCID, "esim_switch_begin")
		p.updateESIMSwitchIdentityGeneration(deviceID, snapshot.SwitchToken, snapshot.IdentityGeneration)
		p.broadcastVoWiFiStateChange(deviceID)
		if worker.QMICore == nil && worker.APDUArbiter != nil {
			worker.APDUArbiter.InvalidateSIMAuthReady("esim_switch_teardown")
		}
		// 创建事件源并注册到 worker，在 APDU 发送前就能接收 UIM indication。
		if worker.Config.ESIMSwitch.EventGatedConverge {
			src := newSwitchEventSource()
			worker.setSwitchEventSource(src)
			logger.Debug("已为切卡创建事件源",
				"device", deviceID,
				"switch_token", snapshot.SwitchToken)
		}
	}
	if snapshot.VoWiFiActiveBefore {
		logger.Info("ESIM 触发切卡，正在为该设备主动注销 VoWiFi 隧道", "device", deviceID)
	}
	if err := p.voWiFiHost().SwitchBegin(context.Background(), deviceID); err != nil {
		logger.Warn("切卡前注销 VoWiFi 隧道失败", "device", deviceID, "err", err)
	}
	if worker := p.GetWorker(deviceID); worker != nil && worker.QMICore != nil {
		if worker.Config.ESIMSwitch.RadioCycle {
			releaseRadioBeforeSwitch(deviceID, worker)
		}
		worker.QMICore.ReleaseAPDULeasesForSwitchTeardown()
		// 切卡前主动清空 DeviceSnapshot 中缓存的 ICCID/IMSI，
		// 防止切卡后其他代码路径从 Snapshot 读到旧卡身份。
		if snap := worker.QMICore.GetDeviceSnapshot(); snap != nil {
			snap.ResetIdentities(false)
		}
	} else if worker := p.GetWorker(deviceID); worker != nil && worker.Config.ESIMSwitch.RadioCycle {
		releaseRadioBeforeSwitch(deviceID, worker)
	}
	return snapshot.SwitchToken
}

func (p *Pool) refreshPostSwitchIdentity(deviceID string, worker *Worker, snapshot esimSwitchContext) (bool, error) {
	pollTimeout := postSwitchIdentityPollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 10 * time.Second
	}
	pollInterval := postSwitchIdentityPollInterval
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	return p.refreshPostSwitchIdentityWithPolling(deviceID, worker, snapshot, pollTimeout, pollInterval)
}

func (p *Pool) refreshPostSwitchIdentityWithPolling(deviceID string, worker *Worker, snapshot esimSwitchContext, pollTimeout time.Duration, pollInterval time.Duration) (bool, error) {
	oldICCID := normalizeSIMIdentity(snapshot.ICCIDBefore)
	oldIMSI := normalizeSIMIdentity(snapshot.IMSIBefore)
	targetICCID := normalizeSIMIdentity(snapshot.TargetICCID)
	targetICCIDKey := normalizeSIMIdentityForCompare(targetICCID)
	oldICCIDKey := normalizeSIMIdentityForCompare(oldICCID)
	worker.EnsureSIMIdentityTransition(targetICCID, "post_switch_finalize")

	reader, ok := worker.Backend.(liveSIMIdentityReader)
	if !ok {
		err := fmt.Errorf("live_identity_not_supported")
		worker.MarkSIMIdentityDegraded("post_switch_finalize", err)
		logger.Warn("切卡后 live 身份读取能力不可用，将按严格门控处理",
			"device", deviceID,
			"reason", "post_switch_finalize",
			"target_iccid", targetICCID,
			"old_iccid", oldICCID,
			"old_imsi", oldIMSI,
			"identity_ready", false,
			"iccid_changed", false,
			"imsi_changed", false,
			"err", err)
		return false, err
	}

	// 切卡后 DMS/UIM 服务内部更新新卡身份需要时间。
	// 单次 GetICCIDLive 可能返回旧卡的值，目标 ICCID 已知时必须等到目标卡生效。
	if pollTimeout <= 0 {
		pollTimeout = 10 * time.Second
	}
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}

	var newICCID, newIMSI string
	var iccidErr, imsiErr error

	pollDeadline := time.Now().Add(pollTimeout)
	for {
		liveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		liveICCID, liveICCIDErr := reader.GetICCIDLive(liveCtx)
		liveIMSI, liveIMSIErr := reader.GetIMSILive(liveCtx)
		cancel()
		newICCID = normalizeSIMIdentity(liveICCID)
		newIMSI = normalizeSIMIdentity(liveIMSI)
		iccidErr = liveICCIDErr
		imsiErr = liveIMSIErr

		newICCIDKey := normalizeSIMIdentityForCompare(newICCID)
		if targetICCIDKey != "" && newICCIDKey == targetICCIDKey {
			// ICCID 已生效，但 IMSI 可能仍在 USIM 重初始化窗口（0x0030/0x0025）中。
			// 为避免把空 IMSI 持久化，等到 IMSI 也成功读出（非空或无错误）再退出。
			if newIMSI != "" && imsiErr == nil {
				break
			}
			// IMSI 尚未就绪，继续轮询直到超时兜底。
		}
		// 如果没有目标 ICCID（例如非 enable 操作），沿用旧语义：无快照或读到不同 ICCID 即认为身份可用。
		if targetICCIDKey == "" && (oldICCIDKey == "" || (newICCIDKey != "" && newICCIDKey != oldICCIDKey)) {
			break
		}
		if time.Now().After(pollDeadline) {
			logger.Warn("切卡后等待新卡身份生效超时",
				"device", deviceID,
				"target_iccid", targetICCID,
				"old_iccid", oldICCID,
				"current_iccid", newICCID,
				"poll_timeout", pollTimeout.String())
			break
		}
		time.Sleep(pollInterval)
	}

	newICCIDKey := normalizeSIMIdentityForCompare(newICCID)
	if targetICCIDKey != "" && newICCIDKey != targetICCIDKey {
		err := fmt.Errorf("post_switch_target_iccid_not_active")
		worker.MarkSIMIdentityDegraded("post_switch_finalize", err)
		logger.Warn("切卡后目标 ICCID 未生效，保留清空后的身份并跳过旧身份覆盖",
			"device", deviceID,
			"reason", "post_switch_finalize",
			"target_iccid", targetICCID,
			"old_iccid", oldICCID,
			"old_imsi", oldIMSI,
			"new_iccid", newICCID,
			"new_imsi", newIMSI,
			"identity_ready", false,
			"iccid_changed", oldICCIDKey != "" && newICCIDKey != "" && oldICCIDKey != newICCIDKey,
			"imsi_changed", oldIMSI != "" && newIMSI != "" && oldIMSI != newIMSI,
			"err", err)
		return false, err
	}

	identityChangedForSPN := oldICCIDKey != "" && newICCIDKey != "" && oldICCIDKey != newICCIDKey
	if !identityChangedForSPN {
		identityChangedForSPN = oldIMSI != "" && newIMSI != "" && oldIMSI != newIMSI
	}

	now := time.Now()
	worker.cacheMu.Lock()
	if iccidErr == nil {
		worker.state.Identity.ICCID = newICCID
		worker.state.Meta.IdentityUpdatedAt = now
		worker.state.Meta.UpdatedAt = now
	}
	if imsiErr == nil {
		worker.state.Identity.IMSI = newIMSI
		worker.state.Meta.IdentityUpdatedAt = now
		worker.state.Meta.UpdatedAt = now
	}
	if identityChangedForSPN {
		worker.state.Identity.NativeSPN = ""
		worker.clearSIMMetadataLocked()
		worker.state.Meta.IdentityUpdatedAt = now
		worker.state.Meta.UpdatedAt = now
	}
	if iccidErr == nil || imsiErr == nil {
		worker.state.Identity.Ready = normalizeSIMIdentity(worker.state.Identity.ICCID) != "" || normalizeSIMIdentity(worker.state.Identity.IMSI) != ""
	}
	if worker.state.Identity.Ready {
		worker.state.Identity.Phase = simIdentityPhaseReady
		worker.state.Identity.TargetICCID = ""
		worker.state.Identity.LastReason = "post_switch_finalize"
		worker.state.Identity.LastError = ""
	}
	worker.cacheMu.Unlock()
	p.PersistIdentityState(worker)

	if iccidErr != nil {
		logger.Warn("切卡后读取 live ICCID 失败，将按严格门控处理",
			"device", deviceID,
			"reason", "post_switch_finalize",
			"err", iccidErr)
	}
	if imsiErr != nil {
		logger.Warn("切卡后读取 live IMSI 失败，变更语义将按空值处理",
			"device", deviceID,
			"reason", "post_switch_finalize",
			"err", imsiErr)
	}

	identityReady := newICCID != ""
	iccidChanged := oldICCIDKey != "" && newICCIDKey != "" && oldICCIDKey != newICCIDKey
	imsiChanged := oldIMSI != "" && newIMSI != "" && oldIMSI != newIMSI
	if !identityReady {
		err := fmt.Errorf("post_switch_iccid_empty")
		worker.MarkSIMIdentityDegraded("post_switch_finalize", err)
		logger.Warn("切卡后严格 ICCID 门控未通过，跳过 VoWiFi 恢复",
			"device", deviceID,
			"reason", "post_switch_finalize",
			"target_iccid", targetICCID,
			"old_iccid", oldICCID,
			"old_imsi", oldIMSI,
			"new_iccid", newICCID,
			"new_imsi", newIMSI,
			"identity_ready", identityReady,
			"iccid_changed", iccidChanged,
			"imsi_changed", imsiChanged)
		return false, err
	}

	logger.Info("切卡后身份刷新完成，VoWiFi 恢复前已同步当前设备身份",
		"device", deviceID,
		"reason", "post_switch_finalize",
		"target_iccid", targetICCID,
		"old_iccid", oldICCID,
		"old_imsi", oldIMSI,
		"new_iccid", newICCID,
		"new_imsi", newIMSI,
		"identity_ready", identityReady,
		"iccid_changed", iccidChanged,
		"imsi_changed", imsiChanged)
	return identityReady, nil
}

func (p *Pool) waitPostSwitchCoreReady(deviceID string, worker *Worker) {
	if err := p.WaitQMIControlReady(deviceID, 30*time.Second); err != nil {
		logger.Warn("切卡后等待控制面就绪超时，继续尝试恢复",
			"device", deviceID, "err", err)
		return
	}
	logger.Info("切卡后控制面已就绪，开始恢复运行态", "device", deviceID)
}

func (p *Pool) newESIMSwitchCallbacks(deviceID string) (func(esim.SwitchOperation, string) uint64, func(uint64), func(uint64, error), func(uint64, esim.SwitchPhase, error), func(uint64, esim.SwitchPhase)) {
	onBefore := func(operation esim.SwitchOperation, targetICCID string) uint64 {
		if operation != esim.SwitchOperationEnableProfile {
			targetICCID = ""
		}
		return p.handleESIMSwitchBefore(deviceID, targetICCID)
	}
	onAfter := func(token uint64) {
		p.handleESIMSwitchAfter(deviceID, token)
	}
	onFailed := func(token uint64, err error) {
		p.handleESIMSwitchFailedWithError(deviceID, token, err)
	}
	onDegraded := func(token uint64, phase esim.SwitchPhase, err error) {
		p.handleESIMSwitchDegradedWithError(deviceID, token, phase, err)
	}
	onPhase := func(token uint64, phase esim.SwitchPhase) {
		p.markESIMSwitchPhaseIfToken(deviceID, token, phase)
	}
	return onBefore, onAfter, onFailed, onDegraded, onPhase
}

func normalizeSIMIdentity(v string) string {
	return strings.TrimSpace(v)
}

func normalizeSIMIdentityForCompare(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, " ", "")
	return strings.TrimRight(v, "F")
}

func (p *Pool) resolvePostSwitchSnapshot(deviceID string) esimSwitchContext {
	snapshot, ok := p.finishESIMSwitch(deviceID)
	if ok {
		return snapshot
	}
	snapshot = p.captureESIMSwitchContext(deviceID, "")
	logger.Warn("切卡后未命中快照，回退到实时状态恢复", "device", deviceID)
	return snapshot
}

func (p *Pool) resolvePostSwitchSnapshotIfToken(deviceID string, token uint64) (esimSwitchContext, bool) {
	if token == 0 {
		return p.resolvePostSwitchSnapshot(deviceID), true
	}
	p.switchMu.Lock()
	snapshot, ok := p.switchContexts[deviceID]
	currentToken := p.switchTokens[deviceID]
	p.switchMu.Unlock()
	if currentToken != token {
		logger.Debug("忽略过期 eSIM 切卡后处理",
			"device", deviceID,
			"switch_token", token,
			"current_switch_token", currentToken)
		return esimSwitchContext{}, false
	}
	if !ok {
		logger.Debug("忽略无快照的 eSIM 切卡后处理",
			"device", deviceID,
			"switch_token", token)
		return esimSwitchContext{}, false
	}
	return snapshot, true
}

func (p *Pool) switchTokenStillCurrent(deviceID string, token uint64, stage string) bool {
	if token == 0 {
		return true
	}
	if p.isLatestSwitchToken(deviceID, token) {
		return true
	}
	p.switchMu.Lock()
	currentToken := p.switchTokens[deviceID]
	p.switchMu.Unlock()
	logger.Debug("停止过期 eSIM 切卡后处理",
		"device", deviceID,
		"stage", strings.TrimSpace(stage),
		"switch_token", token,
		"current_switch_token", currentToken)
	return false
}

func (p *Pool) refreshPostSwitchRuntime(deviceID string, worker *Worker) int64 {
	start := time.Now()
	worker.InvalidateDynamicCache()
	_ = worker.RefreshRuntime(nil, "post_switch_finalize")
	p.PersistRuntimeState(worker)
	p.broadcastVoWiFiStateChange(deviceID)
	return time.Since(start).Milliseconds()
}

func (p *Pool) schedulePostSwitchIdentityRefreshes(deviceID string, snapshot esimSwitchContext) {
	delays := append([]time.Duration(nil), postSwitchIdentityRetryDelays...)
	pollTimeout := postSwitchIdentityPollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 10 * time.Second
	}
	pollInterval := postSwitchIdentityPollInterval
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	for _, delay := range delays {
		delay := delay
		pollTimeout := pollTimeout
		pollInterval := pollInterval
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-p.ctx.Done():
				return
			case <-timer.C:
			}
			worker := p.GetWorker(deviceID)
			if worker == nil || worker.Backend == nil || !worker.SIMIdentityConvergenceMatches(snapshot.TargetICCID, snapshot.IdentityGeneration) {
				return
			}
			_, err := p.refreshPostSwitchIdentityWithPolling(deviceID, worker, snapshot, pollTimeout, pollInterval)
			if err != nil {
				logger.Debug("切卡后补刷新 SIM 身份失败", "device", deviceID, "delay", delay.String(), "err", err)
				return
			}
			p.PersistIdentityState(worker)
			p.broadcastVoWiFiStateChange(deviceID)
			logger.Debug("切卡后补刷新 SIM 身份完成", "device", deviceID, "delay", delay.String())
		}()
	}
}

type postSwitchSIMAuthProbeResult struct {
	Ready  bool
	Stage  string
	AID    string
	Source string
	Err    error
}

func resolvePostSwitchSIMAuthAID(ctx context.Context, auth backend.SIMAuthProvider) (aid string, source string, err error) {
	fallback := postSwitchSIMAuthUSIMAID
	if auth == nil {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: auth unavailable")
	}
	resolver, ok := auth.(backend.SIMAuthAIDResolver)
	if !ok {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: resolver unavailable")
	}
	resolvedAID, resolvedSource, err := resolver.ResolveSIMAuthAID(ctx, "usim", fallback)
	if err != nil {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: %w", err)
	}
	resolvedAID = strings.ToUpper(strings.TrimSpace(resolvedAID))
	if resolvedAID == "" {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: empty resolved USIM AID")
	}
	if !strings.HasPrefix(resolvedAID, fallback) {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: resolved USIM AID prefix mismatch: %s", resolvedAID)
	}
	if len(resolvedAID) <= len(fallback) {
		return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: resolved USIM AID is not full AID: %s", resolvedAID)
	}
	if resolvedSource == "" {
		resolvedSource = "resolver"
	}
	return resolvedAID, resolvedSource, nil
}

func probePostSwitchSIMAuthReady(ctx context.Context, auth backend.SIMAuthProvider) postSwitchSIMAuthProbeResult {
	if auth == nil {
		return postSwitchSIMAuthProbeResult{Ready: true}
	}

	aid, aidSource, err := resolvePostSwitchSIMAuthAID(ctx, auth)
	if err != nil {
		return postSwitchSIMAuthProbeResult{Stage: "resolve_usim_full_aid", Err: err}
	}

	return postSwitchSIMAuthProbeResult{Ready: true, Stage: "usim_full_aid_ready", Err: nil, AID: aid, Source: aidSource}
}

func postSwitchSIMAuthProbeWithTimeout(auth backend.SIMAuthProvider) postSwitchSIMAuthProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return probePostSwitchSIMAuthReady(ctx, auth)
}

func postSwitchSIMAuthNotReadyError(result postSwitchSIMAuthProbeResult) error {
	if result.Err != nil {
		if result.Stage != "" {
			return fmt.Errorf("post_switch_sim_auth_not_ready probe_stage=%s: %w", result.Stage, result.Err)
		}
		return fmt.Errorf("post_switch_sim_auth_not_ready: %w", result.Err)
	}
	if result.Stage != "" {
		return fmt.Errorf("post_switch_sim_auth_not_ready probe_stage=%s", result.Stage)
	}
	return fmt.Errorf("post_switch_sim_auth_not_ready")
}

func (p *Pool) prewarmPostSwitchSIMAuth(deviceID string, worker *Worker) postSwitchSIMAuthProbeResult {
	if worker == nil || worker.Backend == nil {
		return postSwitchSIMAuthProbeResult{Ready: true}
	}
	auth, ok := worker.Backend.(backend.SIMAuthProvider)
	if !ok {
		logger.Debug("切卡后跳过 SIMAuth 预热：backend 不支持 SIMAuthProvider",
			"device", deviceID,
			"backend", worker.Backend.Mode())
		return postSwitchSIMAuthProbeResult{Ready: true}
	}

	backendMode := worker.Backend.Mode()
	result := postSwitchSIMAuthProbeWithTimeout(auth)
	if result.Ready {
		return result
	}

	logger.Warn("切卡后 VoWiFi 恢复前 SIMAuth full AID 未就绪，开始等待",
		"device", deviceID,
		"backend", backendMode,
		"probe_stage", result.Stage,
		"err", result.Err)

	for attempt, delay := range postSwitchSIMAuthRecoveryDelays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-p.ctx.Done():
				timer.Stop()
				return result
			case <-timer.C:
			}
		}
		result = postSwitchSIMAuthProbeWithTimeout(auth)
		if result.Ready {
			logger.Info("切卡后 SIMAuth 轻量软恢复完成",
				"device", deviceID,
				"backend", backendMode,
				"attempt", attempt+1,
				"probe_stage", result.Stage)
			return result
		}
	}

	err := postSwitchSIMAuthNotReadyError(result)
	logger.Warn("切卡后 SIMAuth 预热未就绪，暂缓 VoWiFi 恢复",
		"device", deviceID,
		"backend", backendMode,
		"probe_stage", result.Stage,
		"err", err)
	return result
}

func (p *Pool) waitPostSwitchSIMAuthReady(deviceID string, worker *Worker) error {
	if worker == nil || worker.Backend == nil || !worker.Config.VoWiFiEnabled {
		return nil
	}
	configureWorkerAPDUArbiter(worker, nil)
	if worker.APDUArbiter == nil {
		result := p.prewarmPostSwitchSIMAuth(deviceID, worker)
		if result.Ready {
			return nil
		}
		return postSwitchSIMAuthNotReadyError(result)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), postSwitchSIMAuthReadyWaitTimeout)
	defer cancel()
	err := worker.APDUArbiter.WaitSIMAuthReady(waitCtx, func(ctx context.Context) error {
		result := p.prewarmPostSwitchSIMAuth(deviceID, worker)
		if result.Ready {
			return nil
		}
		return postSwitchSIMAuthNotReadyError(result)
	})
	if err != nil {
		logger.Warn("切卡后 SIMAuth gate 未就绪，暂缓 VoWiFi 恢复",
			"device", deviceID,
			"err", err)
		return err
	}
	logger.Info("切卡后 SIMAuth 逻辑通道已就绪，开始恢复 VoWiFi", "device", deviceID)
	return nil
}

func (p *Pool) restorePostSwitchConnectivity(deviceID string, worker *Worker, snapshot esimSwitchContext, restoreGateErr error, markDegraded bool) {
	if worker.Config.VoWiFiEnabled {
		if restoreGateErr != nil {
			if markDegraded {
				p.markESIMSwitchPhase(deviceID, esim.SwitchPhaseDegraded)
			}
			logger.Warn("切卡后跳过 VoWiFi 恢复：恢复门控未通过",
				"device", deviceID,
				"reason", "post_switch_finalize",
				"err", restoreGateErr)
		} else {
			p.clearDesiredVoWiFiRecoverState(deviceID)
			if err := p.voWiFiHost().SwitchEnd(context.Background(), deviceID, true); err != nil {
				p.markESIMSwitchPhase(deviceID, esim.SwitchPhaseDegraded)
				logger.Error("切卡后恢复 VoWiFi 失败", "device", deviceID, "err", err)
			} else {
				return
			}
		}
	}
	p.restoreRadioDataForSwitchSnapshot(deviceID, worker, snapshot, "post_switch_finalize", worker.Config.ESIMSwitch.RadioCycle)
}

func (p *Pool) setOperatingModeWithRetry(worker *Worker, mode backend.OperatingMode) error {
	delays := []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		if i > 0 {
			select {
			case <-p.ctx.Done():
				return lastErr
			case <-time.After(delays[i-1]):
			}
		}
		lastErr = worker.Backend.SetOperatingMode(p.ctx, mode)
		if lastErr == nil {
			return nil
		}
		if !isPostSwitchQMIStallError(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (p *Pool) restoreRadioDataForSwitchSnapshot(deviceID string, worker *Worker, snapshot esimSwitchContext, reason string, radioCycleAlreadyDone bool) {
	if snapshot.FlightModeBefore {
		if worker.Backend != nil {
			if err := p.setOperatingModeWithRetry(worker, backend.ModeRFOff); err != nil {
				logger.Warn("切卡后维持飞行模式失败", "device", deviceID, "reason", reason, "err", err)
			}
		}
		if nc := worker.NetworkController(); nc != nil && nc.IsConnected() {
			if err := worker.StopNetwork(); err != nil {
				logger.Warn("切卡后关闭数据连接失败", "device", deviceID, "reason", reason, "err", err)
			}
		}
		return
	}

	// 如果 RadioCycle 已经在 bringRadioOnlineAfterSwitch 中处理过，不再重复设置在线模式。
	if !radioCycleAlreadyDone && worker.Backend != nil {
		if err := p.setOperatingModeWithRetry(worker, backend.ModeOnline); err != nil {
			logger.Warn("切卡后恢复在线模式失败", "device", deviceID, "reason", reason, "err", err)
		}
	}

	if err := p.applyNetworkPreferenceForSwitchSnapshot(worker, snapshot); err != nil {
		logger.Warn("切卡后按快照恢复网络失败", "device", deviceID, "reason", reason, "err", err)
	}
	if nc := worker.NetworkController(); nc != nil && nc.IsConnected() {
		p.refreshIPs(worker, true)
	}
}

func (p *Pool) handleESIMSwitchFailed(deviceID string, token uint64) {
	snapshot, ok := p.finishESIMSwitchForFailure(deviceID, token)
	if !ok {
		return
	}
	worker := p.GetWorker(deviceID)
	if worker == nil {
		logger.Warn("eSIM 切卡失败收尾：设备不存在，已清理切卡状态", "device", deviceID)
		return
	}
	logger.Warn("eSIM 切卡失败，按切卡前快照恢复 radio/data",
		"device", deviceID,
		"switch_token", snapshot.SwitchToken,
		"switch_phase", snapshot.Phase,
		"flight_before", snapshot.FlightModeBefore,
		"qmi_connected_before", snapshot.QMIConnectedBefore,
		"network_enabled_before", snapshot.NetworkEnabledBefore)
	p.restoreRadioDataForSwitchSnapshot(deviceID, worker, snapshot, "switch_failed", false)
	p.broadcastVoWiFiStateChange(deviceID)
}

func (p *Pool) handleESIMSwitchFailedWithError(deviceID string, token uint64, err error) {
	p.handleESIMSwitchFailed(deviceID, token)
}

func (p *Pool) handleESIMSwitchDegradedWithError(deviceID string, token uint64, phase esim.SwitchPhase, err error) {
	if phase == "" {
		phase = esim.SwitchPhaseDegraded
	}
	p.markESIMSwitchPhaseIfToken(deviceID, token, phase)
}

func (p *Pool) finishESIMSwitchForFailure(deviceID string, token uint64) (esimSwitchContext, bool) {
	if token == 0 {
		snapshot, ok := p.finishESIMSwitch(deviceID)
		if ok && snapshot.SwitchToken != 0 {
			p.clearESIMSwitchIfToken(deviceID, snapshot.SwitchToken)
			return snapshot, true
		}
		p.clearESIMSwitch(deviceID)
		if !ok {
			snapshot = p.captureESIMSwitchContext(deviceID, "")
		}
		return snapshot, true
	}

	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	currentToken := p.switchTokens[deviceID]
	if currentToken != token {
		logger.Debug("忽略过期 eSIM 切卡失败收尾",
			"device", deviceID,
			"switch_token", token,
			"current_switch_token", currentToken)
		return esimSwitchContext{}, false
	}
	snapshot, ok := p.switchContexts[deviceID]
	if !ok {
		logger.Debug("忽略无快照的 eSIM 切卡失败收尾",
			"device", deviceID,
			"switch_token", token)
		return esimSwitchContext{}, false
	}
	delete(p.switchContexts, deviceID)
	delete(p.switchingDevices, deviceID)
	delete(p.switchTokens, deviceID)
	return snapshot, true
}

func (p *Pool) handleESIMSwitchAfter(deviceID string, token uint64) {
	finalizeStart := time.Now()
	snapshot, ok := p.resolvePostSwitchSnapshotIfToken(deviceID, token)
	if !ok {
		return
	}
	defer func() {
		if snapshot.SwitchToken != 0 {
			p.clearESIMSwitchIfToken(deviceID, snapshot.SwitchToken)
			return
		}
		p.clearESIMSwitch(deviceID)
	}()
	finalizeOK := false
	defer func() {
		if finalizeOK {
			p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseDone)
		}
	}()

	worker := p.GetWorker(deviceID)
	if worker == nil {
		p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseFailed)
		logger.Warn("切卡后恢复失败：设备不存在", "device", deviceID)
		return
	}
	snapshot.IdentityGeneration = worker.EnsureSIMIdentityTransition(snapshot.TargetICCID, "post_switch_finalize")
	p.updateESIMSwitchIdentityGeneration(deviceID, snapshot.SwitchToken, snapshot.IdentityGeneration)
	p.broadcastVoWiFiStateChange(deviceID)

	coreWaitStart := time.Now()
	p.waitPostSwitchCoreReady(deviceID, worker)
	coreReadyWaitMS := time.Since(coreWaitStart).Milliseconds()
	if !p.switchTokenStillCurrent(deviceID, token, "core_ready") {
		return
	}

	logger.Info("ESIM 切卡后开始按快照恢复运行态",
		"device", deviceID,
		"vowifi_switch", worker.Config.VoWiFiEnabled,
		"vowifi_before", snapshot.VoWiFiActiveBefore,
		"flight_before", snapshot.FlightModeBefore,
		"qmi_connected_before", snapshot.QMIConnectedBefore,
		"network_enabled_before", snapshot.NetworkEnabledBefore)

	convergence := p.runPostSwitchConvergence(deviceID, token, worker, snapshot)
	if convergence.Degraded {
		p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseDegraded)
		p.schedulePostSwitchIdentityRefreshes(deviceID, snapshot)
		p.restorePostSwitchConnectivity(deviceID, worker, snapshot, fmt.Errorf("%s", convergence.Reason), false)
		return
	}

	if worker.Config.ESIMSwitch.RadioCycle {
		attachTimeout := time.Duration(worker.Config.ESIMSwitch.NASAttachTimeoutMS) * time.Millisecond
		p.bringRadioOnlineAfterSwitch(deviceID, worker, snapshot, attachTimeout)
		if !p.switchTokenStillCurrent(deviceID, token, "radio_online") {
			return
		}
	}

	p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseIdentityRefresh)
	identityRefreshStart := time.Now()
	identityReady, identityRefreshErr := p.refreshPostSwitchIdentity(deviceID, worker, snapshot)
	identityRefreshMS := time.Since(identityRefreshStart).Milliseconds()
	if !p.switchTokenStillCurrent(deviceID, token, "identity_refresh") {
		return
	}
	p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseRuntimeRestore)
	runtimeRefreshMS := p.refreshPostSwitchRuntime(deviceID, worker)
	if !p.switchTokenStillCurrent(deviceID, token, "runtime_restore") {
		return
	}

	logger.Info("切卡后 finalize 阶段耗时",
		"device", deviceID,
		"identity_ready", identityReady,
		"core_ready_wait_ms", coreReadyWaitMS,
		"identity_refresh_ms", identityRefreshMS,
		"runtime_refresh_ms", runtimeRefreshMS,
		"finalize_total_ms", time.Since(finalizeStart).Milliseconds())

	if identityRefreshErr != nil {
		p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseDegraded)
		p.schedulePostSwitchIdentityRefreshes(deviceID, snapshot)
		p.restorePostSwitchConnectivity(deviceID, worker, snapshot, identityRefreshErr, false)
		return
	}
	p.schedulePostSwitchIdentityRefreshes(deviceID, snapshot)
	var restoreGateErr error
	if worker.Config.VoWiFiEnabled {
		simAuthReadyStart := time.Now()
		restoreGateErr = p.waitPostSwitchSIMAuthReady(deviceID, worker)
		logger.Info("切卡后 SIMAuth gate 阶段耗时",
			"device", deviceID,
			"ready", restoreGateErr == nil,
			"sim_auth_ready_ms", time.Since(simAuthReadyStart).Milliseconds())
		if !p.switchTokenStillCurrent(deviceID, token, "simauth_ready") {
			return
		}
	}
	p.markESIMSwitchPhaseIfToken(deviceID, token, esim.SwitchPhaseVoWiFiRestore)
	if !p.switchTokenStillCurrent(deviceID, token, "vowifi_restore") {
		return
	}
	p.restorePostSwitchConnectivity(deviceID, worker, snapshot, restoreGateErr, true)

	// 切卡过程中 SIM power cycle 会导致 overview 缓存被清空且重载失败（模组正在重置），
	// 此处模组已恢复，触发一次 overview 重新加载以恢复 profile 列表。
	if worker.EsimMgr != nil {
		worker.EsimMgr.WarmOverviewAsync("post_switch_finalize")
	}
	finalizeOK = true
}
