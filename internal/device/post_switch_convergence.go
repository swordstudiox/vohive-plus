package device

import (
	"context"
	"errors"
	"strings"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type postSwitchAction string

const (
	postSwitchActionWaitControl    postSwitchAction = "wait_control"
	postSwitchActionWaitSIM        postSwitchAction = "wait_sim"
	postSwitchActionWaitIdentity   postSwitchAction = "wait_identity"
	postSwitchActionRestoreRuntime postSwitchAction = "restore_runtime"
)

type postSwitchDecision struct {
	Action postSwitchAction
	Reason string
}

func classifyPostSwitchReadiness(r qmimanager.UIMReadiness, targetICCID string) postSwitchDecision {
	target := normalizeSIMIdentityForCompare(targetICCID)
	current := normalizeSIMIdentityForCompare(r.ICCID)
	reason := string(r.Reason)
	if reason == "" {
		reason = "unknown"
	}
	if !r.TransportReady || !r.ControlReady {
		return postSwitchDecision{Action: postSwitchActionWaitControl, Reason: reason}
	}
	if !r.UIMReady {
		return postSwitchDecision{Action: postSwitchActionWaitSIM, Reason: reason}
	}
	if strings.TrimSpace(target) != "" && current != target {
		return postSwitchDecision{Action: postSwitchActionWaitIdentity, Reason: "target_iccid_not_active"}
	}
	if strings.TrimSpace(r.ICCID) == "" && strings.TrimSpace(r.IMSI) == "" {
		return postSwitchDecision{Action: postSwitchActionWaitIdentity, Reason: "identity_empty"}
	}
	return postSwitchDecision{Action: postSwitchActionRestoreRuntime, Reason: reason}
}

type postSwitchReadinessProvider interface {
	GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error)
}

type postSwitchSIMReloader interface {
	UIMPostSwitchReload(ctx context.Context, readiness qmimanager.UIMReadiness, opts qmimanager.UIMPostSwitchReloadOptions) (uint8, error)
}

type postSwitchSIMPowerController interface {
	UIMPowerOffSIM(ctx context.Context, slot uint8) error
	UIMPowerOnSIM(ctx context.Context, slot uint8) error
}

type postSwitchCoreRecoveryRequester interface {
	RequestCoreRecovery(reason string) bool
}

type postSwitchCoreReadyWaiter interface {
	WaitCoreReady(ctx context.Context) error
}

type postSwitchOperatingModeProbe interface {
	GetOperatingMode(ctx context.Context) (backend.OperatingMode, error)
}

type postSwitchSignalProbe interface {
	GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error)
}

type postSwitchConvergenceOptions struct {
	TargetICCID         string
	IdentityAttempts    int
	ReloadAfterAttempts int
	ProbeTimeout        time.Duration
	CoreStallThreshold  int
	ReinitWindow        time.Duration // expected reinit window; service stalls inside it do not trigger whole-core recovery
}

type postSwitchConvergenceResult struct {
	Ready    bool
	Degraded bool
	Reason   string
	Slot     uint8
}

const (
	defaultPostSwitchProbeTimeout       = 1500 * time.Millisecond
	defaultPostSwitchCoreStallThreshold = 2
	defaultPostSwitchCoreReadyWait      = 15 * time.Second
)

func isPostSwitchQMIStallError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	for _, fragment := range []string{
		"context deadline exceeded",
		"timeout",
		"timed out",
		"qmi 服务未就绪",
		"service not ready",
		"service unavailable",
		"device busy",
		"device or resource busy",
		"qmi: read failed",
		"read failed: eof",
		"broken pipe",
		"client closed",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

func isPostSwitchQMIServiceStall(r qmimanager.UIMReadiness, err error) bool {
	if err == nil {
		err = r.Err
	}
	if r.Reason == qmimanager.UIMReadinessTransportFatal {
		return true
	}
	if !r.TransportReady {
		return isPostSwitchQMIStallError(err)
	}
	if r.Reason == qmimanager.UIMReadinessControlUnavailable || !r.ControlReady {
		return isPostSwitchQMIStallError(err)
	}
	return isPostSwitchQMIStallError(err)
}

func probePostSwitchQMIServiceStall(ctx context.Context, probe any) (bool, error) {
	if dms, ok := probe.(postSwitchOperatingModeProbe); ok {
		if _, err := dms.GetOperatingMode(ctx); isPostSwitchQMIStallError(err) {
			return true, err
		}
	}
	if nas, ok := probe.(postSwitchSignalProbe); ok {
		if _, err := nas.GetSignalInfo(ctx); isPostSwitchQMIStallError(err) {
			return true, err
		}
	}
	return false, nil
}

func convergePostSwitch(ctx context.Context, readiness postSwitchReadinessProvider, reloader any, opts postSwitchConvergenceOptions) postSwitchConvergenceResult {
	if readiness == nil {
		return postSwitchConvergenceResult{Degraded: true, Reason: "uim_readiness_not_supported"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.IdentityAttempts <= 0 {
		opts.IdentityAttempts = 20
	}
	if opts.ReloadAfterAttempts <= 0 {
		opts.ReloadAfterAttempts = 6
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = defaultPostSwitchProbeTimeout
	}
	if opts.CoreStallThreshold <= 0 {
		opts.CoreStallThreshold = defaultPostSwitchCoreStallThreshold
	}
	var reinitWindowEnd time.Time
	if opts.ReinitWindow > 0 {
		reinitWindowEnd = time.Now().Add(opts.ReinitWindow)
	}
	var last postSwitchDecision
	var lastReadiness qmimanager.UIMReadiness
	consecutiveServiceStalls := 0
	for attempt := 1; attempt <= opts.IdentityAttempts; attempt++ {
		probeCtx := ctx
		cancel := func() {}
		if opts.ProbeTimeout > 0 {
			probeCtx, cancel = context.WithTimeout(ctx, opts.ProbeTimeout)
		}
		r, err := readiness.GetUIMReadiness(probeCtx)
		cancel()
		if err != nil {
			r.Err = err
		}
		lastReadiness = r
		last = classifyPostSwitchReadiness(r, opts.TargetICCID)
		switch last.Action {
		case postSwitchActionRestoreRuntime:
			return postSwitchConvergenceResult{Ready: true, Reason: last.Reason, Slot: r.ActiveSlot}
		}
		serviceStall := isPostSwitchQMIServiceStall(r, err)
		serviceStallErr := err
		if !serviceStall {
			probeCtx := ctx
			probeCancel := func() {}
			if opts.ProbeTimeout > 0 {
				probeCtx, probeCancel = context.WithTimeout(ctx, opts.ProbeTimeout)
			}
			serviceStall, serviceStallErr = probePostSwitchQMIServiceStall(probeCtx, reloader)
			probeCancel()
		}
		inReinitWindow := opts.ReinitWindow > 0 && time.Now().Before(reinitWindowEnd)
		if serviceStall && !inReinitWindow {
			consecutiveServiceStalls++
			if consecutiveServiceStalls >= opts.CoreStallThreshold {
				if recoverer, ok := reloader.(postSwitchCoreRecoveryRequester); ok {
					requested := recoverer.RequestCoreRecovery("post_switch_qmi_service_stalled")
					stalls := consecutiveServiceStalls
					consecutiveServiceStalls = 0
					if requested {
						logger.Warn("切卡后探测到 QMI service 假死特征，已提前请求 core recovery",
							"attempt", attempt,
							"stalls", stalls,
							"reason", last.Reason,
							"err", serviceStallErr)
					} else {
						// requested=false 说明 coreReady 已为 false（有其他恢复在途）或正在停止。
						// 不论哪种，仍需等待 core 收敛，不能直接判 degraded。
						logger.Debug("切卡后 QMI service 假死特征已达到阈值，core recovery 请求被拒（已有恢复在途），等待收敛",
							"attempt", attempt,
							"stalls", stalls,
							"reason", last.Reason,
							"err", serviceStallErr)
					}
					if waiter, ok := reloader.(postSwitchCoreReadyWaiter); ok {
						if requested {
							// RequestCoreRecovery 是异步的：它只是向 channel 发送了一个事件。
							// recovery goroutine 需要时间启动并调用 markCoreNotReady。
							// 如果不等待，WaitCoreReady 会因 coreReady 仍为 true 而瞬间返回。
							time.Sleep(1 * time.Second)
						}
						// 不论是否由本侧发起恢复，均等待 core 收敛：
						// requested=false 时 coreReady 已为 false，WaitCoreReady 直接等信号即可。
						logger.Info("切卡后正在等待 Core Recovery 收敛完成",
							"attempt", attempt,
							"requested", requested,
							"timeout", defaultPostSwitchCoreReadyWait.String())
						waitCtx, waitCancel := context.WithTimeout(ctx, defaultPostSwitchCoreReadyWait)
						if waitErr := waiter.WaitCoreReady(waitCtx); waitErr != nil {
							logger.Warn("切卡后等待 Core Recovery 收敛超时或失败",
								"attempt", attempt,
								"err", waitErr)
						} else {
							logger.Info("切卡后 Core Recovery 收敛成功",
								"attempt", attempt,
								"requested", requested)
						}
						waitCancel()
					} else if requested {
						// WaitCoreReady 接口不可用，硬退避 8 秒等待底层 QMI 重建完成
						logger.Warn("切卡后 WaitCoreReady 接口不可用，硬退避 8 秒等待 Core Recovery 完成",
							"attempt", attempt,
							"waiter_available", false)
						time.Sleep(8 * time.Second)
					}
					continue
				}
			}
		} else {
			consecutiveServiceStalls = 0
		}
		if attempt == opts.ReloadAfterAttempts && r.TransportReady && r.ControlReady {
			if composite, ok := reloader.(postSwitchSIMReloader); ok {
				// power-cycle 后由 UIMPostSwitchReload 内部统一收敛 provisioning。
				slot, err := composite.UIMPostSwitchReload(ctx, r, qmimanager.UIMPostSwitchReloadOptions{DefaultSlot: 1})
				if err != nil {
					return postSwitchConvergenceResult{Degraded: true, Reason: "reload_degraded", Slot: slot}
				}
				continue
			}
			if power, ok := reloader.(postSwitchSIMPowerController); ok {
				targetSlot := uint8(1)
				if r.SlotKnown && r.ActiveSlot != 0 {
					targetSlot = r.ActiveSlot
				}
				if err := triggerPowerCycleFallback(ctx, power, targetSlot); err != nil {
					return postSwitchConvergenceResult{Degraded: true, Reason: "reload_degraded", Slot: targetSlot}
				}
			}
		}
	}
	if lastReadiness.ActiveSlot != 0 {
		return postSwitchConvergenceResult{Degraded: true, Reason: "identity_convergence_timeout", Slot: lastReadiness.ActiveSlot}
	}
	return postSwitchConvergenceResult{Degraded: true, Reason: last.Reason}
}

func effectivePostSwitchReinitWindow(cfg config.ESIMSwitchConfig) time.Duration {
	if !cfg.EventGatedConverge {
		return 0
	}
	if cfg.ReinitWindowMS > 0 {
		return time.Duration(cfg.ReinitWindowMS) * time.Millisecond
	}
	return 5 * time.Second
}

func (p *Pool) runPostSwitchConvergence(deviceID string, token uint64, worker *Worker, snapshot esimSwitchContext) postSwitchConvergenceResult {
	if worker == nil || worker.Backend == nil {
		return postSwitchConvergenceResult{Degraded: true, Reason: "worker_or_backend_missing"}
	}
	readiness, ok := worker.Backend.(postSwitchReadinessProvider)
	if !ok {
		return postSwitchConvergenceResult{Degraded: true, Reason: "uim_readiness_not_supported"}
	}
	var reloader any = worker.Backend
	reinitWindow := effectivePostSwitchReinitWindow(worker.Config.ESIMSwitch)
	if worker.Config.ESIMSwitch.EventGatedConverge {
		src := worker.currentSwitchEventSource()
		// 如果事件源未被创建（例如 runPostSwitchConvergence 被直接调用），则在此创建。
		if src == nil {
			src = newSwitchEventSource()
			worker.setSwitchEventSource(src)
		}
		defer func() {
			if src != nil {
				worker.setSwitchEventSource(nil)
				src.Close()
			}
		}()

		switch awaitReinitConverged(context.Background(), src, readiness, snapshot.TargetICCID, reinitWindow) {
		case reinitConverged:
			logger.Info("eSIM 切卡后事件门控收敛成功",
				"device", deviceID,
				"switch_token", token,
				"target_iccid", snapshot.TargetICCID,
				"reinit_window", reinitWindow.String())
			p.resolveAndApplyPolicy(worker, "esim_switched")
			return postSwitchConvergenceResult{Ready: true, Reason: "ready"}
		case reinitTimeout:
			if power, ok := reloader.(postSwitchSIMPowerController); ok {
				slot := uint8(1)
				probeCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				if r, err := readiness.GetUIMReadiness(probeCtx); err == nil && r.SlotKnown && r.ActiveSlot != 0 {
					slot = r.ActiveSlot
				}
				cancel()
				if err := triggerPowerCycleFallback(context.Background(), power, slot); err != nil {
					logger.Warn("eSIM 切卡后事件门控超时，受控 UIM 重启失败",
						"device", deviceID,
						"switch_token", token,
						"slot", slot,
						"err", err)
				} else {
					logger.Info("eSIM 切卡后事件门控超时，已执行受控 UIM 重启 fallback",
						"device", deviceID,
						"switch_token", token,
						"slot", slot)
				}
			}
		}
	}
	result := convergePostSwitch(context.Background(), readiness, reloader, postSwitchConvergenceOptions{
		TargetICCID:         snapshot.TargetICCID,
		IdentityAttempts:    20,
		ReloadAfterAttempts: 6,
		ReinitWindow:        reinitWindow,
	})
	logger.Info("eSIM 切卡后 SIM 收敛结果",
		"device", deviceID,
		"switch_token", token,
		"ready", result.Ready,
		"degraded", result.Degraded,
		"reason", result.Reason,
		"slot", result.Slot)
	if result.Ready {
		p.resolveAndApplyPolicy(worker, "esim_switched")
	}
	return result
}
