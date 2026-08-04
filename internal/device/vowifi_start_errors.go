package device

import (
	"errors"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func logVoWiFiFailureSummary(traceID, deviceID, stage, errorClass, reason string, retryable bool, nextRetry time.Duration) {
	if strings.TrimSpace(errorClass) == "" {
		errorClass = "unknown"
	}
	logger.Warn("VoWiFi 失败汇总",
		"trace_id", traceID,
		"device", deviceID,
		"stage", stage,
		"error_class", errorClass,
		"reason", reason,
		"retryable", retryable,
		"next_retry", nextRetry.String())
}

func (p *Pool) handleVoWiFiStartupError(traceID, deviceID, runtimeEPDGOverride string, generation uint64, enableStart time.Time, w *Worker, state runtimehost.State, err error) error {
	defer p.clearVoWiFiStartupStateAndBroadcast(deviceID)
	if errors.Is(err, apduarbiter.ErrAPDUBusy) {
		logger.Debug("VoWiFi 启动遇到 APDU busy，等待短退避恢复",
			"trace_id", traceID,
			"device", deviceID,
			"err", err)
		p.scheduleVoWiFiAPDUBusyRecover(deviceID, runtimeEPDGOverride, generation)
		p.restoreNetworkAfterVoWiFiStartupFailure(traceID, deviceID, w)
		logger.Debug("EnableVoWiFi 结束（APDU busy）", "trace_id", traceID, "device", deviceID, "cost_ms", time.Since(enableStart).Milliseconds())
		return err
	}

	logger.Error("VoWiFi 启动失败", "trace_id", traceID, "device", deviceID, "err", err)
	retryable := shouldRetryVoWiFiAutoStart(err)
	nextRetry := vowifihost.DesiredRecoverDelay(0)
	if !retryable {
		nextRetry = 0
	}
	logVoWiFiFailureSummary(traceID, deviceID, "startup", state.LastErrorClass, err.Error(), retryable, nextRetry)
	p.restoreNetworkAfterVoWiFiStartupFailure(traceID, deviceID, w)
	logger.Debug("EnableVoWiFi 结束（失败）", "trace_id", traceID, "device", deviceID, "cost_ms", time.Since(enableStart).Milliseconds())
	return err
}

func (p *Pool) restoreNetworkAfterVoWiFiStartupFailure(traceID, deviceID string, w *Worker) {
	if w == nil {
		return
	}
	defer func() {
		w.restoreNetworkAfterVoWiFi = false
	}()
	nc := w.NetworkController()
	if nc == nil || !w.restoreNetworkAfterVoWiFi || w.Backend == nil {
		return
	}
	if restoreErr := w.Backend.SetOperatingMode(p.ctx, backend.ModeOnline); restoreErr != nil {
		logger.Warn("恢复射频失败", "trace_id", traceID, "device", deviceID, "err", restoreErr)
	}
	time.Sleep(500 * time.Millisecond)
	if connectErr := nc.Connect(); connectErr != nil {
		logger.Warn("恢复数据连接失败", "trace_id", traceID, "device", deviceID, "err", connectErr)
	}
}

func shouldRetryVoWiFiAutoStart(err error) bool {
	if err == nil {
		return false
	}
	return !carrier.IsVoWiFiPolicyBlockedError(err)
}

func (p *Pool) scheduleVoWiFiAPDUBusyRecover(deviceID, overrideEPDG string, generation uint64) {
	if p == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	for _, delay := range []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second} {
		delay := delay
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-p.ctx.Done():
				return
			case <-timer.C:
			}
			if p.IsVoWiFiActive(deviceID) {
				return
			}
			if err := p.voWiFiHost().Recover(p.ctx, vowifihost.LifecycleRecoverRequest{
				DeviceID:     deviceID,
				Reason:       "apdu_busy",
				OverrideEPDG: strings.TrimSpace(overrideEPDG),
				Generation:   generation,
			}); err != nil {
				logger.Debug("VoWiFi APDU busy 短退避恢复提交失败", "device", deviceID, "delay", delay.String(), "err", err)
			}
		}()
	}
}
