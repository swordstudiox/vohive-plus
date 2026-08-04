package device

import (
	"context"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

var qmiIdentityConvergenceTimeout = 2 * time.Minute
var qmiConvergenceRetryInterval = 2 * time.Second

// qmiConvergenceTransportFailureLimit 是收敛期间允许的连续传输断开次数上限，
// 达到后判定 qmi-proxy 控制面已失联，升级为完整 Worker 重建。
var qmiConvergenceTransportFailureLimit = 3

var convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
	return p.refreshModemRebootRecoveredIdentity(w, reason)
}

var convergeEscalateFn func(*Pool, *Worker, string, error)

func defaultConvergeEscalate(p *Pool, w *Worker, reason string, err error) {
	p.handleTransportRecoveryExhausted(w, w.generation, HealthLayerQMI, reason, err)
}

func qmiConvergenceShouldEscalate(transportFailureStreak, limit int) bool {
	if limit <= 0 {
		limit = 1
	}
	return transportFailureStreak >= limit
}

func (p *Pool) convergeQMIIdentity(ctx context.Context, worker *Worker, reason string) error {
	if p == nil || worker == nil {
		return context.Canceled
	}
	if ctx == nil {
		ctx = p.ctx
	}
	ticker := time.NewTicker(qmiConvergenceRetryInterval)
	defer ticker.Stop()

	transportFailures := 0
	for {
		err := convergeIdentityRefreshFn(p, worker, reason)
		if err == nil {
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerQMI,
				State:     HealthStateHealthy,
				EventType: "qmi_identity_ready",
				Reason:    reason,
			})
			logger.Info("QMI 身份收敛完成", "device", worker.ID, "reason", reason)
			return nil
		}

		if qmiErrorIndicatesTransportDown(err.Error()) {
			transportFailures++
			if qmiConvergenceShouldEscalate(transportFailures, qmiConvergenceTransportFailureLimit) {
				logger.Warn("QMI 身份收敛检测到控制面持续断开，升级为 Worker 重建",
					"device", worker.ID,
					"reason", reason,
					"transport_failures", transportFailures,
					"err", err)
				escalateQMIConvergence(p, worker, "convergence_transport_down", err)
				return err
			}
		} else {
			transportFailures = 0
		}

		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				logger.Warn("QMI 身份收敛超时，升级为 Worker 重建",
					"device", worker.ID,
					"reason", reason)
				escalateQMIConvergence(p, worker, "convergence_timeout", ctx.Err())
			}
			return ctx.Err()
		case <-worker.stop:
			return context.Canceled
		case <-ticker.C:
		}
	}
}

func escalateQMIConvergence(p *Pool, w *Worker, reason string, err error) {
	if convergeEscalateFn != nil {
		convergeEscalateFn(p, w, reason, err)
		return
	}
	defaultConvergeEscalate(p, w, reason, err)
}

func (p *Pool) startQMIIdentityConvergence(worker *Worker, reason string) {
	if p == nil || worker == nil {
		return
	}
	worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:     HealthLayerQMI,
		State:     HealthStateRecovering,
		EventType: "qmi_identity_converging",
		Reason:    reason,
	})
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, qmiIdentityConvergenceTimeout)
		defer cancel()
		if err := p.convergeQMIIdentity(ctx, worker, reason); err != nil {
			logger.Warn("QMI 身份收敛未完成", "device", worker.ID, "reason", reason, "err", err)
		} else {
			p.resolveAndApplyPolicy(worker, "identity_ready")
		}
	}()
}
