package device

import (
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

var atRadioWarmupDelays = []time.Duration{0, time.Second, 3 * time.Second, 8 * time.Second}

func (p *Pool) scheduleATRadioWarmup(worker *Worker, reason string) {
	if p == nil || worker == nil || worker.Backend == nil || worker.Backend.Mode() != backend.BackendAT {
		return
	}
	delays := append([]time.Duration(nil), atRadioWarmupDelays...)
	go func() {
		for i, delay := range delays {
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-p.ctx.Done():
					timer.Stop()
					return
				case <-worker.stop:
					timer.Stop()
					return
				case <-timer.C:
				}
			} else {
				select {
				case <-p.ctx.Done():
					return
				case <-worker.stop:
					return
				default:
				}
			}
			if err := worker.RefreshRuntime(nil, "startup_radio_warmup"); err != nil {
				logger.Debug("AT radio 启动预热失败", "device", worker.ID, "attempt", i+1, "reason", reason, "err", err)
				continue
			}
			p.broadcastVoWiFiStateChange(worker.ID)
		}
	}()
}
