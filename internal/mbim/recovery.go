package mbimcore

import (
	"context"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

// OnRecoveryExhausted registers a callback fired when host-side control-plane
// reopen attempts are exhausted and the device still cannot be recovered in
// process. The device layer bridges this to a full worker rebuild.
func (m *Manager) OnRecoveryExhausted(cb func(reason string, err error)) {
	m.mu.Lock()
	m.recoveryExhausted = cb
	m.mu.Unlock()
}

func (m *Manager) dispatchRecoveryExhausted(reason string, err error) {
	m.mu.Lock()
	cb := m.recoveryExhausted
	m.mu.Unlock()
	if cb != nil {
		cb(reason, err)
	}
}

// triggerReopen starts a single-flight recovery supervisor. Concurrent calls
// while a recovery is in progress are dropped.
func (m *Manager) triggerReopen(reason string) {
	if !m.recoveryGate.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.recoveryGate.Store(false)
		if hook := m.runRecoveryHook; hook != nil {
			hook(reason)
			return
		}
		m.runRecovery(reason)
	}()
}

// runRecovery performs up to healthMaxReopenAttempts host-side reopens. On the
// first reopen that yields a working control plane it returns; if all attempts
// fail it fires OnRecoveryExhausted.
func (m *Manager) runRecovery(reason string) {
	backoff := m.reopenBackoff
	if backoff <= 0 {
		backoff = m.healthProbeInterval
	}
	if backoff <= 0 {
		backoff = defaultHealthProbeInterval
	}
	timeout := m.reopenTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= healthMaxReopenAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := m.doReopen(ctx)
		if err == nil {
			err = m.verifyControlPlane(ctx)
		}
		cancel()
		if err == nil {
			logger.Info("[mbim] 控制面 reopen 恢复成功",
				"control_device", m.controlDevice,
				"attempt", attempt,
				"reason", reason)
			return
		}
		lastErr = err
		logger.Warn("[mbim] 控制面 reopen 失败",
			"control_device", m.controlDevice,
			"attempt", attempt,
			"reason", reason,
			"err", err)
		if attempt < healthMaxReopenAttempts {
			time.Sleep(backoff)
		}
	}
	logger.Warn("[mbim] 控制面 reopen 耗尽，转交 worker 重建",
		"control_device", m.controlDevice,
		"reason", reason,
		"err", lastErr)
	m.dispatchRecoveryExhausted(reason, lastErr)
}

// doReopen tears the control endpoint down and brings it back up, restoring the
// data session if one was desired.
func (m *Manager) doReopen(ctx context.Context) error {
	m.mu.Lock()
	wantData := m.desiredConnection
	m.mu.Unlock()

	_ = m.Close()
	if err := m.Open(ctx); err != nil {
		return err
	}
	if wantData {
		if err := m.Connect(); err != nil {
			return err
		}
	}
	return nil
}

// verifyControlPlane probes the freshly reopened device with a cheap read.
func (m *Manager) verifyControlPlane(ctx context.Context) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	vctx, cancel := context.WithTimeout(ctx, m.probeTimeoutOrDefault())
	defer cancel()
	_, err = mbim.QueryRegisterState(vctx, d)
	return err
}

func (m *Manager) probeTimeoutOrDefault() time.Duration {
	if m.healthProbeTimeout > 0 {
		return m.healthProbeTimeout
	}
	return defaultHealthProbeTimeout
}
