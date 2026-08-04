package mbimcore

import (
	"context"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

type HealthEventState string

const (
	HealthEventHealthy    HealthEventState = "healthy"
	HealthEventSuspect    HealthEventState = "suspect"
	HealthEventRecovering HealthEventState = "recovering"
)

type HealthEvent struct {
	State  HealthEventState
	Reason string
	At     time.Time
}

const (
	defaultHealthProbeInterval    = 30 * time.Second
	defaultHealthProbeTimeout     = 5 * time.Second
	healthSuspectFailureThreshold = 2
	healthRecoverFailureThreshold = 3
	healthMaxReopenAttempts       = 3
)

func (m *Manager) OnHealth(cb func(HealthEvent)) {
	m.mu.Lock()
	m.healthCB = cb
	m.mu.Unlock()
}

func (m *Manager) startHealthProbe() {
	m.mu.Lock()
	interval := m.healthProbeInterval
	if interval <= 0 {
		interval = defaultHealthProbeInterval
	}
	done := m.healthDone
	exited := m.healthLoopExited
	m.mu.Unlock()

	go func() {
		defer close(exited)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				m.runHealthProbe()
			}
		}
	}()
}

// fireHealth invokes the health callback outside the lock.
func (m *Manager) fireHealth(event HealthEvent) {
	m.mu.Lock()
	cb := m.healthCB
	m.mu.Unlock()
	if cb != nil {
		cb(event)
	}
}

func (m *Manager) triggerReopenHookOrNil() func(string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.triggerReopenHook
}

func (m *Manager) runHealthProbe() {
	d, err := m.device()
	if err != nil {
		return
	}

	m.mu.Lock()
	timeout := m.healthProbeTimeout
	if timeout <= 0 {
		timeout = defaultHealthProbeTimeout
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	_, probeErr := mbim.QueryRegisterState(ctx, d)
	cancel()

	m.mu.Lock()
	prevState := m.healthState
	var event HealthEvent
	fire := false
	startReopen := false
	if probeErr == nil {
		m.healthFailures = 0
		if prevState != HealthEventHealthy {
			event = HealthEvent{State: HealthEventHealthy, Reason: "register_state_query_ok", At: time.Now()}
			m.healthState = HealthEventHealthy
			fire = true
		}
	} else {
		m.healthFailures++
		switch {
		case m.healthFailures >= healthRecoverFailureThreshold && prevState != HealthEventRecovering:
			event = HealthEvent{State: HealthEventRecovering, Reason: probeErr.Error(), At: time.Now()}
			m.healthState = HealthEventRecovering
			fire = true
			startReopen = true
		case m.healthFailures >= healthSuspectFailureThreshold && prevState != HealthEventSuspect && prevState != HealthEventRecovering:
			event = HealthEvent{State: HealthEventSuspect, Reason: probeErr.Error(), At: time.Now()}
			m.healthState = HealthEventSuspect
			fire = true
		}
	}
	m.mu.Unlock()

	if fire {
		m.fireHealth(event)
	}
	if startReopen {
		hook := m.triggerReopenHookOrNil()
		if hook != nil {
			hook(event.Reason)
		} else {
			m.triggerReopen(event.Reason)
		}
	}
}
