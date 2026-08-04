package vowifihost

import (
	"context"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type TeardownOptions struct {
	Reason         string
	RestoreSMS     bool
	RestoreSMSMode func(deviceID string)
	SkipInvalidate bool
}

func (m *Manager) StopInstanceForTeardown(ctx context.Context, deviceID, reason string) bool {
	if m == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "teardown"
	}
	inst := m.RuntimeStore().Instance(deviceID)
	if inst == nil {
		return false
	}

	if ctx == nil {
		ctx = context.Background()
	}
	stopCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		stopCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	if err := inst.Stop(stopCtx); err != nil {
		logger.Warn("VoWiFi teardown 失败", "device", deviceID, "reason", reason, "err", err)
	}
	m.RuntimeStore().DeleteInstance(deviceID, inst)
	m.BroadcastState(deviceID)
	return true
}

func (m *Manager) TeardownSession(ctx context.Context, deviceID string, opts TeardownOptions) bool {
	if m == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		reason = "teardown"
	}
	if !opts.SkipInvalidate {
		m.InvalidateRuntime(deviceID, reason)
	}
	if !m.StopInstanceForTeardown(ctx, deviceID, reason) {
		return false
	}
	if opts.RestoreSMS {
		if opts.RestoreSMSMode != nil {
			opts.RestoreSMSMode(deviceID)
		} else if adapter := m.hostAdapter(); adapter != nil {
			adapter.RestoreSMSMode(deviceID)
		}
	}
	return true
}

func (m *Manager) TeardownForReconnect(ctx context.Context, deviceID string) bool {
	if !m.TeardownSession(ctx, deviceID, TeardownOptions{Reason: "reconnect", RestoreSMS: true}) {
		return false
	}
	logger.Info("模块掉线，已拆除旧 VoWiFi 实例", "device", strings.TrimSpace(deviceID))
	return true
}

func (m *Manager) TeardownForSwitch(ctx context.Context, deviceID string) bool {
	if !m.TeardownSession(ctx, deviceID, TeardownOptions{Reason: "switch", RestoreSMS: true}) {
		return false
	}
	logger.Info("eSIM 切卡前已拆除旧 VoWiFi 实例", "device", strings.TrimSpace(deviceID))
	return true
}

func (m *Manager) InvalidateRuntime(deviceID, reason string) uint64 {
	if m == nil {
		return 0
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return 0
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "invalidate"
	}
	epoch, hadStartupState := m.RuntimeStore().Invalidate(deviceID)
	logger.Debug("VoWiFi runtime epoch invalidated", "device", deviceID, "reason", reason, "epoch", epoch)
	if hadStartupState {
		m.BroadcastState(deviceID)
	}
	return epoch
}
