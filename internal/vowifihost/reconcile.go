package vowifihost

import (
	"context"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const defaultDesiredRecoverReason = "desired_reconcile"

type DesiredRecoverRequest struct {
	DeviceID     string
	Reason       string
	OverrideEPDG string
	Generation   uint64
	Now          time.Time
	OnResult     func(deviceID, reason string, err error)
}

func (m *Manager) DesiredRecoverable(deviceID string) bool {
	if m == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	store := m.RuntimeStore()
	return !store.Active(deviceID) && !store.Starting(deviceID)
}

func (m *Manager) ScheduleDesiredRecover(ctx context.Context, req DesiredRecoverRequest) bool {
	if m == nil {
		return false
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return false
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = defaultDesiredRecoverReason
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !m.DesiredRecoverable(deviceID) {
		return false
	}
	if !m.BeginDesiredRecover(deviceID, now) {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	logger.Warn("VoWiFi 目标态恢复开始", "event", "VOWIFI_DESIRED_RECOVER", "device", deviceID, "reason", reason)
	go func() {
		err := m.Recover(ctx, LifecycleRecoverRequest{
			DeviceID:     deviceID,
			Reason:       reason,
			OverrideEPDG: req.OverrideEPDG,
			Generation:   req.Generation,
		})
		if req.OnResult != nil {
			req.OnResult(deviceID, reason, err)
		}
	}()
	return true
}
