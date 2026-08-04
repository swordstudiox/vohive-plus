package device

import (
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const vowifiInitialAutoStartReason = "startup_auto"

// startInitialDesiredVoWiFiAutoStart schedules the configured VoWiFi desired
// state after startup without letting one device's lifecycle block the rest.
func (p *Pool) startInitialDesiredVoWiFiAutoStart(delay time.Duration) {
	if p == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	go func() {
		ctx := p.Context()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			p.scheduleInitialDesiredVoWiFiStarts(now)
		}
	}()
}

func (p *Pool) scheduleInitialDesiredVoWiFiStarts(now time.Time) {
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
	if len(candidates) == 0 {
		return
	}
	logger.Info("启动期调度 VoWiFi 期望态自动拉起", "count", len(candidates), "reason", vowifiInitialAutoStartReason)
	for _, deviceID := range candidates {
		p.scheduleDesiredVoWiFiRecover(deviceID, vowifiInitialAutoStartReason, now)
	}
}
