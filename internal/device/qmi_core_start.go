package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const qmiCoreStartupInlineBudget = 1500 * time.Millisecond
const qmiCoreRetryAttemptBudget = 15 * time.Second

type qmiCoreStartResult struct {
	err   error
	retry bool
	abort bool
}

func runQMIStartCoreAttempt(parent context.Context, startCore func(context.Context) error, budget time.Duration) qmiCoreStartResult {
	if parent == nil {
		parent = context.Background()
	}
	if budget <= 0 {
		budget = qmiCoreStartupInlineBudget
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()

	err := startCore(ctx)
	if err == nil {
		return qmiCoreStartResult{}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return qmiCoreStartResult{err: err, retry: true}
	}
	if qmiStartCoreFailureShouldAbortWorker(err.Error()) {
		return qmiCoreStartResult{err: err, abort: true}
	}
	return qmiCoreStartResult{err: err, retry: true}
}

func runQMIStartCoreRetryAttempt(parent context.Context, startCore func(context.Context) error, budget time.Duration) error {
	if parent == nil {
		parent = context.Background()
	}
	if budget <= 0 {
		budget = qmiCoreRetryAttemptBudget
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	return startCore(ctx)
}

func (p *Pool) startQMICoreWithStartupBudget(worker *Worker, reason string) error {
	if worker == nil || worker.QMICore == nil {
		return nil
	}
	if p.lifecycle != nil {
		p.lifecycle.BeginRecovery(worker.ID, LifecyclePhaseQMIStarting, reason, qmiLifecycleRecoveryTTL)
	}

	result := runQMIStartCoreAttempt(p.ctx, worker.QMICore.StartCoreContext, qmiCoreStartupInlineBudget)
	if result.err == nil {
		cleanupWorkerStartupSIMAuthLogicalChannels(worker)
		if _, resetErr := p.resetExistingQMIDataConnectionBeforePreference(worker, reason); resetErr != nil {
			logger.Warn(fmt.Sprintf("[%s] QMI Core 启动后清理已有数据连接失败，继续启动", worker.ID), "err", resetErr)
		}
		p.markQMIControlRecovered(worker, reason)
		logger.Debug(fmt.Sprintf("[%s] QMI Core 已启动，网络偏好将异步应用", worker.ID))
		return nil
	}
	if result.abort {
		return result.err
	}

	logger.Warn(fmt.Sprintf("[%s] 启动 QMI Core 未就绪，转入后台重试", worker.ID),
		"err", result.err,
		"startup_budget", qmiCoreStartupInlineBudget.String())
	p.startQMICoreRetryLoop(worker)
	return nil
}

func (p *Pool) startQMICoreRetryLoop(worker *Worker) {
	if worker == nil || worker.QMICore == nil {
		return
	}
	go func() {
		delay := 2 * time.Second
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-worker.stop:
				return
			case <-time.After(delay):
			}

			if err := runQMIStartCoreRetryAttempt(p.ctx, worker.QMICore.StartCoreContext, qmiCoreRetryAttemptBudget); err == nil {
				logger.Info(fmt.Sprintf("[%s] QMI Core 已恢复启动", worker.ID))
				cleanupWorkerStartupSIMAuthLogicalChannels(worker)
				if _, resetErr := p.resetExistingQMIDataConnectionBeforePreference(worker, "qmi_core_recovered"); resetErr != nil {
					logger.Warn(fmt.Sprintf("[%s] QMI Core 恢复后清理既有数据连接失败，跳过自动应用网络偏好", worker.ID), "err", resetErr)
				} else {
					if applyErr := p.applyNetworkPreference(worker); applyErr != nil {
						logger.Warn(fmt.Sprintf("[%s] QMI Core 恢复后自动应用网络偏好失败", worker.ID), "err", applyErr)
					}
				}
				p.markQMIControlRecovered(worker, "qmi_core_recovered")
				return
			} else {
				if errors.Is(err, context.Canceled) {
					return
				}
				if delay < 60*time.Second {
					delay *= 2
					if delay > 60*time.Second {
						delay = 60 * time.Second
					}
				}
				logger.Warn(fmt.Sprintf("[%s] 启动 QMI Core 失败(重试中)", worker.ID), "err", err, "next_retry_in", delay.String())
			}
		}
	}()
}
