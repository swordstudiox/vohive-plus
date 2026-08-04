package device

import (
	"context"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	qmipkg "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

var startupSIMAuthLogicalChannelsToClose = []int{1, 2, 3, 4}

type startupUIMResetter interface {
	UIMReset(ctx context.Context) error
}

type startupSIMStatusSource interface {
	GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error)
}

type startupProvisioningEnsurer interface {
	EnsureSIMProvisioned(ctx context.Context, opts qmimanager.EnsureSIMProvisionedOptions) (qmimanager.UIMReadiness, error)
}

// 编译期保证 *qmipkg.Manager 满足 ensurer 接口；签名漂移将直接 break build 而非静默跳过。
var _ startupProvisioningEnsurer = (*qmipkg.Manager)(nil)

func cleanupWorkerStartupSIMAuthLogicalChannels(w *Worker) {
	if w == nil || w.Backend == nil {
		return
	}
	if w.QMICore != nil {
		performStartupQMIUIMReset(w.ID, w.QMICore, w.QMICore, startupQMISIMReadyCheck(w.QMICore), 5*time.Second, 250*time.Millisecond)
		return
	}
	auth, ok := w.Backend.(backend.SIMAuthProvider)
	if !ok {
		logger.Debug("启动期跳过 SIMAuth 逻辑通道清理：backend 不支持 SIMAuthProvider",
			"device", w.ID)
		return
	}
	for _, channelID := range startupSIMAuthLogicalChannelsToClose {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := auth.CloseLogicalChannel(closeCtx, channelID)
		cancel()
		if err != nil {
			logger.Debug("启动期 SIMAuth 逻辑通道清理失败",
				"device", w.ID,
				"backend", w.Backend.Mode(),
				"channel", channelID,
				"err", err)
		}
	}
}

func startupQMISIMReadyCheck(source startupSIMStatusSource) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		if source == nil {
			return false, nil
		}
		status, err := source.GetSIMStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == qmi.SIMReady, nil
	}
}

func performStartupQMIUIMReset(deviceID string, resetter startupUIMResetter, ensurer startupProvisioningEnsurer, readyCheck func(context.Context) (bool, error), waitTimeout, pollInterval time.Duration) bool {
	if resetter == nil {
		return false
	}
	resetCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := resetter.UIMReset(resetCtx)
	cancel()
	if err != nil {
		logger.Debug("启动期 QMI UIM reset 失败",
			"device", deviceID,
			"err", err)
		return false
	}
	logger.Debug("启动期 QMI UIM reset 已完成",
		"device", deviceID)
	if ensurer != nil {
		ensureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, err := ensurer.EnsureSIMProvisioned(ensureCtx, qmimanager.EnsureSIMProvisionedOptions{}); err != nil {
			logger.Debug("启动期 QMI provisioning 收敛 best-effort 失败", "device", deviceID, "err", err)
		}
		cancel()
	}
	if readyCheck == nil {
		return true
	}
	if waitTimeout <= 0 {
		waitTimeout = 5 * time.Second
	}
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(waitTimeout)
	for {
		checkCtx, cancel := context.WithTimeout(context.Background(), pollInterval)
		ready, err := readyCheck(checkCtx)
		cancel()
		if err == nil && ready {
			logger.Debug("启动期 QMI UIM reset 后 SIM ready",
				"device", deviceID)
			return true
		}
		if time.Now().After(deadline) {
			logger.Warn("启动期 QMI UIM reset 后等待 SIM ready 超时",
				"device", deviceID,
				"timeout", waitTimeout.String(),
				"last_err", err)
			return false
		}
		time.Sleep(pollInterval)
	}
}
