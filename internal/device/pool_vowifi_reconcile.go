package device

import (
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/carrier"

	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const (
	vowifiDesiredReconcileInterval = 30 * time.Second
	vowifiDesiredReconcileReason   = "desired_reconcile"
)

// startVoWiFiDesiredReconcileLoop 定期检查配置期望态，把丢失的 VoWiFi 实例低频拉回。
func (p *Pool) startVoWiFiDesiredReconcileLoop() {
	if p == nil {
		return
	}
	ticker := time.NewTicker(vowifiDesiredReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case now := <-ticker.C:
			p.reconcileDesiredVoWiFiOnce(now)
		}
	}
}

// reconcileDesiredVoWiFiOnce 扫描所有 worker，找出配置仍希望开启但当前未运行 VoWiFi 的设备。
func (p *Pool) reconcileDesiredVoWiFiOnce(now time.Time) {
	if p == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}

	p.mu.RLock()
	workers := make([]*Worker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}
	p.mu.RUnlock()

	candidates := make([]string, 0, len(workers))
	for _, w := range workers {
		if p.shouldReconcileVoWiFi(w) {
			candidates = append(candidates, w.ID)
		}
	}
	for _, deviceID := range candidates {
		p.scheduleDesiredVoWiFiRecover(deviceID, vowifiDesiredReconcileReason, now)
	}
}

// shouldReconcileVoWiFi 判断设备是否允许进入目标态恢复队列。
func (p *Pool) shouldReconcileVoWiFi(w *Worker) bool {
	return p.shouldReconcileVoWiFiForReason(w, "")
}

func (p *Pool) shouldReconcileVoWiFiForReason(w *Worker, reason string) bool {
	if p == nil || w == nil {
		return false
	}
	deviceID := strings.TrimSpace(w.ID)
	if deviceID == "" {
		return false
	}
	if p.isWorkerRebuilding(deviceID) {
		return false
	}
	if p.IsESIMSwitching(deviceID) {
		return false
	}

	if !p.voWiFiHost().DesiredRecoverable(deviceID) {
		return false
	}

	status := w.ProjectDeviceStatus()
	iccid := strings.TrimSpace(status.ICCID)
	imsi := strings.TrimSpace(status.IMSI)
	if imsi == "" {
		imsi = strings.TrimSpace(w.GetCachedIMSI())
	}
	reason = strings.TrimSpace(reason)
	identityRequired := reason == "" ||
		reason == vowifiDesiredReconcileReason ||
		reason == vowifiInitialAutoStartReason
	if identityRequired && iccid == "" && imsi == "" {
		logger.Warn("VoWiFi 目标态恢复跳过：SIM 身份未就绪", "event", "VOWIFI_DESIRED_RECOVER_SKIPPED_IDENTITY", "device", deviceID)
		return false
	}
	if !p.currentCardPolicyAllowsVoWiFi(w, status.ICCID, reason) {
		return false
	}
	mcc, _, _ := vowifiProfileMCCMNC(status)
	if mcc != "" && carrier.IsVoWiFiBlockedMCC(mcc) {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		logger.Warn("VoWiFi 目标态恢复跳过：MCC 策略禁止", "event", "VOWIFI_DESIRED_RECOVER_SKIPPED_POLICY", "device", deviceID, "mcc", formatVoWiFiPLMN3(mcc), "imsi", imsi)
		return false
	}
	return true
}

func (p *Pool) isWorkerRebuilding(deviceID string) bool {
	if p == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	p.mu.RLock()
	rebuilding := p.rebuilding[deviceID]
	p.mu.RUnlock()
	return rebuilding
}

func (p *Pool) currentCardPolicyAllowsVoWiFi(w *Worker, statusICCID, reason string) bool {
	if p == nil || w == nil {
		return false
	}
	deviceID := strings.TrimSpace(w.ID)
	iccid := strings.TrimSpace(w.ConfirmedICCID())
	if iccid == "" {
		if deviceID != "" {
			p.clearDesiredVoWiFiRecoverState(deviceID)
		}
		logger.Warn("VoWiFi 目标态恢复跳过：ICCID 未就绪，无法解析卡策略",
			"event", "VOWIFI_DESIRED_RECOVER_SKIPPED_CARD_POLICY",
			"device", deviceID,
			"reason", strings.TrimSpace(reason))
		return false
	}
	p.mu.RLock()
	resolver := p.policyResolver
	p.mu.RUnlock()
	if resolver == nil {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		logger.Warn("VoWiFi 目标态恢复跳过：卡策略解析器未配置",
			"event", "VOWIFI_DESIRED_RECOVER_SKIPPED_CARD_POLICY",
			"device", deviceID,
			"iccid", iccid,
			"reason", strings.TrimSpace(reason))
		return false
	}
	pol, err := resolver.Resolve(iccid)
	if err != nil {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		logger.Warn("VoWiFi 目标态恢复跳过：解析卡策略失败",
			"event", "VOWIFI_DESIRED_RECOVER_SKIPPED_CARD_POLICY",
			"device", deviceID,
			"iccid", iccid,
			"reason", strings.TrimSpace(reason),
			"err", err)
		return false
	}
	if !pol.VoWiFiEnabled {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		// logger.Info("VoWiFi 目标态恢复跳过：当前卡策略未开启 VoWiFi",
		// 	"event", "VOWIFI_DESIRED_RECOVER_SKIPPED_CARD_POLICY",
		// 	"device", deviceID,
		// 	"iccid", iccid,
		// 	"reason", strings.TrimSpace(reason))
		return false
	}
	return true
}

// scheduleDesiredVoWiFiRecover 按设备退避状态排队一次恢复，真正执行仍走生命周期串行控制器。
func (p *Pool) scheduleDesiredVoWiFiRecover(deviceID, reason string, now time.Time) bool {
	if p == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	if reason = strings.TrimSpace(reason); reason == "" {
		reason = vowifiDesiredReconcileReason
	}
	if now.IsZero() {
		now = time.Now()
	}

	return p.voWiFiHost().ScheduleDesiredRecover(p.ctx, vowifihost.DesiredRecoverRequest{
		DeviceID: deviceID,
		Reason:   reason,
		Now:      now,
		OnResult: func(deviceID, _ string, err error) {
			p.markDesiredVoWiFiRecoverResult(deviceID, err)
		},
	})
}

// markDesiredVoWiFiRecoverResult 根据恢复结果清理状态或安排下一次低频重试。
func (p *Pool) markDesiredVoWiFiRecoverResult(deviceID string, err error) {
	if p == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	if err == nil {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		logger.Info("VoWiFi 目标态恢复成功", "event", "VOWIFI_DESIRED_RECOVER_SUCCESS", "device", deviceID)
		return
	}
	if carrier.IsVoWiFiPolicyBlockedError(err) {
		p.clearDesiredVoWiFiRecoverState(deviceID)
		logger.Warn("VoWiFi 目标态恢复跳过：策略禁止", "event", "VOWIFI_DESIRED_RECOVER_SKIPPED_POLICY", "device", deviceID, "err", err)
		return
	}
	snapshot := p.voWiFiHost().MarkDesiredRecoverFailed(deviceID, time.Now(), err)

	logger.Warn("VoWiFi 目标态恢复失败，等待低频重试", "event", "VOWIFI_DESIRED_RETRY_DELAY", "device", deviceID, "attempt", snapshot.Attempt, "delay", snapshot.Delay.String(), "err", err)
}

// clearDesiredVoWiFiRecoverState 清除设备的目标态恢复退避状态。
func (p *Pool) clearDesiredVoWiFiRecoverState(deviceID string) {
	if p == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	p.voWiFiHost().ClearDesiredRecoverState(deviceID)
}
