package vowifihost

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/engine/swu"
	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const lifecycleReadyTimeout = 3 * time.Second

type runtimeEnableRequest struct {
	DeviceID     string
	AllowSwitch  bool
	OverrideEPDG string
	Generation   uint64
	Reason       string
}

func (m *Manager) runLifecycleCommand(ctx context.Context, cmd LifecycleCommand) error {
	if m == nil {
		return fmt.Errorf("vowifi host manager is nil")
	}
	switch cmd.Kind {
	case LifecycleCommandEnable:
		return m.enableRuntime(ctx, runtimeEnableRequest{
			DeviceID:     cmd.DeviceID,
			AllowSwitch:  cmd.AllowSwitch,
			OverrideEPDG: cmd.OverrideEPDG,
			Generation:   cmd.Generation,
			Reason:       cmd.Reason,
		})
	case LifecycleCommandDisable:
		return m.disableRuntime(ctx, cmd.DeviceID, cmd.Reason, cmd.RestoreRadio, cmd.RuntimeInvalidated)
	case LifecycleCommandRestart:
		return m.restartRuntime(ctx, cmd.DeviceID, cmd.Generation)
	case LifecycleCommandRecover:
		return m.recoverRuntime(ctx, cmd.DeviceID, cmd.Reason, cmd.Generation, cmd.OverrideEPDG)
	case LifecycleCommandSwitchBegin:
		m.TeardownForSwitch(ctx, cmd.DeviceID)
		return nil
	case LifecycleCommandSwitchEnd:
		if cmd.RestoreRadio {
			return m.enableRuntime(ctx, runtimeEnableRequest{
				DeviceID:    cmd.DeviceID,
				AllowSwitch: true,
				Generation:  cmd.Generation,
				Reason:      "switch_restore",
			})
		}
		return nil
	default:
		return fmt.Errorf("unsupported vowifi lifecycle command kind: %d", int(cmd.Kind))
	}
}

func (m *Manager) enableRuntime(ctx context.Context, req runtimeEnableRequest) (retErr error) {
	adapter := m.hostAdapter()
	if adapter == nil {
		return fmt.Errorf("vowifi host adapter is not configured")
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return fmt.Errorf("vowifi enable device_id is empty")
	}
	if !req.AllowSwitch && adapter.IsSwitching(deviceID) {
		return fmt.Errorf("设备 %s 正在切卡，暂不允许启动 VoWiFi", deviceID)
	}

	traceID := runtimehost.NewTraceID()
	baseCtx := hostContext(ctx, adapter)
	startCtx := runtimehost.WithTraceID(baseCtx, traceID)
	startedAt := time.Now()

	startClaim := m.BeginStart(deviceID)
	if startClaim.Active {
		return nil
	}
	if startClaim.Starting {
		return fmt.Errorf("设备 %s 的 VoWiFi 正在启动中", deviceID)
	}
	if !startClaim.Accepted {
		return fmt.Errorf("设备 %s 的 VoWiFi 启动声明失败", deviceID)
	}
	startupEpoch := startClaim.Epoch
	startFinalized := false
	defer func() {
		if !startFinalized {
			m.FailStart(deviceID, startupEpoch, runtimehost.State{}, retErr)
		}
	}()

	preparedStart, err := m.PrepareStart(deviceID, traceID, req.OverrideEPDG)
	if err != nil {
		return err
	}
	generation := m.ensureLifecycleGeneration(deviceID, req.Generation, req.Reason)
	modemIface := preparedStart.Modem
	if modemIface == nil {
		return fmt.Errorf("设备 %s 的 VoWiFi modem adapter 未准备", deviceID)
	}
	initialState := preparedStart.StartupState

	result, err := m.StartRuntime(startCtx, RuntimeStartRequest{
		DeviceID:      deviceID,
		TraceID:       traceID,
		Epoch:         startupEpoch,
		Prepared:      preparedStart,
		Modem:         modemIface,
		VoiceGateway:  m.voiceGateway,
		Dataplane:     runtimehost.DataplanePolicy{Mode: swu.DataplaneModeUserspace},
		DeliveryStore: m.deliveryStore,
		Dispatch:      m.dispatcher,
		BeforeStart:   m.BeforeStart(deviceID, modemIface, preparedStart.Proxy),
	})
	if err != nil {
		state, ok := m.State(deviceID)
		if !ok {
			state = initialState
		}
		return adapter.HandleStartupError(StartupErrorRequest{
			TraceID:             traceID,
			DeviceID:            deviceID,
			RuntimeEPDGOverride: strings.TrimSpace(req.OverrideEPDG),
			Generation:          generation,
			StartedAt:           startedAt,
			State:               state,
			Err:                 err,
		})
	}

	if result.Stale {
		startFinalized = true
		logger.Info("VoWiFi 启动完成但运行态已被更新取代，已关闭该旧实例",
			"trace_id", traceID,
			"device", deviceID,
			"generation", generation,
			"cost_ms", time.Since(startedAt).Milliseconds())
		return nil
	}

	activeCount := 0
	if m.Active(deviceID) {
		activeCount = 1
	}
	startFinalized = true
	m.ClearStartupStateAndBroadcast(deviceID)
	adapter.MarkRuntimeStarted(RuntimeStartedRequest{
		TraceID:     traceID,
		DeviceID:    deviceID,
		ActiveCount: activeCount,
		Elapsed:     time.Since(startedAt),
	})
	logger.Info("VoWiFi 已启用、短信模式已切换为 VoWiFi", "trace_id", traceID, "device", deviceID, "active_count", activeCount)
	logger.Debug("EnableVoWiFi 结束（成功）", "trace_id", traceID, "device", deviceID, "cost_ms", time.Since(startedAt).Milliseconds())
	return nil
}

func (m *Manager) disableRuntime(ctx context.Context, reasonDeviceID, reason string, restoreRadio, skipInvalidate bool) error {
	adapter := m.hostAdapter()
	if adapter == nil {
		return fmt.Errorf("vowifi host adapter is not configured")
	}
	if ctx == nil {
		ctx = hostContext(nil, adapter)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "disable"
	}

	var devIDs []string
	if target := strings.TrimSpace(reasonDeviceID); target != "" {
		devIDs = []string{target}
	} else {
		devIDs = m.InstanceIDs()
	}

	for _, devID := range devIDs {
		stopped := m.TeardownSession(ctx, devID, TeardownOptions{
			Reason:         reason,
			RestoreSMS:     true,
			SkipInvalidate: skipInvalidate,
		})
		if !stopped {
			logger.Info("VoWiFi 实例不存在，执行强制恢复流程", "device", devID, "reason", reason, "restore_radio", restoreRadio)
			adapter.RestoreSMSMode(devID)
		}
		if restoreRadio {
			if err := adapter.RestoreRadioAfterVoWiFi(devID); err != nil {
				logger.Warn("VoWiFi 停用后恢复射频失败", "device", devID, "reason", reason, "err", err)
			}
		}
		logger.Info("VoWiFi 已禁用、QMI/AT 短信轮询已恢复，当前未自动恢复射频/数据", "device", devID, "reason", reason)
	}
	return nil
}

func (m *Manager) restartRuntime(ctx context.Context, deviceID string, generation uint64) error {
	adapter := m.hostAdapter()
	if adapter == nil {
		return fmt.Errorf("vowifi host adapter is not configured")
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("vowifi restart device_id is empty")
	}
	if !adapter.WorkerExists(deviceID) {
		return fmt.Errorf("设备 %s 不存在，无法重启 VoWiFi", deviceID)
	}

	logger.Info("准备重启 VoWiFi 隧道...", "device", deviceID)
	if err := m.disableRuntime(ctx, deviceID, "restart", false, false); err != nil {
		logger.Warn("停用旧 VoWiFi 隧道时发生错误", "device", deviceID, "err", err)
	}
	if err := m.enableWhenReady(ctx, deviceID, lifecycleReadyTimeout, "restart", "", generation); err != nil {
		return fmt.Errorf("重启 VoWiFi 失败: %w", err)
	}
	logger.Info("VoWiFi 重启流程执行完成", "device", deviceID)
	return nil
}

func (m *Manager) recoverRuntime(ctx context.Context, deviceID, reason string, generation uint64, overrideEPDG string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("vowifi recover device_id is empty")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "recover"
	}
	if m.Active(deviceID) || m.Starting(deviceID) {
		logger.Debug("VoWiFi recover 跳过：运行态仍活跃或正在启动", "device", deviceID, "reason", reason)
		return nil
	}
	_ = m.TeardownSession(ctx, deviceID, TeardownOptions{Reason: reason, RestoreSMS: true})
	if err := m.enableWhenReady(ctx, deviceID, lifecycleReadyTimeout, reason, overrideEPDG, generation); err != nil {
		return fmt.Errorf("恢复 VoWiFi 失败(%s): %w", reason, err)
	}
	return nil
}

func (m *Manager) enableWhenReady(ctx context.Context, deviceID string, timeout time.Duration, reason, overrideEPDG string, generation uint64) error {
	adapter := m.hostAdapter()
	if adapter == nil {
		return fmt.Errorf("vowifi host adapter is not configured")
	}
	if generation == 0 {
		logger.Debug("VoWiFi ready-enable received zero generation; runtime fallback may allocate one", "device", strings.TrimSpace(deviceID), "reason", strings.TrimSpace(reason))
	}
	if err := adapter.WaitQMICoreReady(deviceID, timeout); err != nil {
		return fmt.Errorf("等待设备 %s QMI Core 就绪失败(%s): %w", deviceID, reason, err)
	}
	if err := adapter.WaitWorkerReady(deviceID, timeout); err != nil {
		return fmt.Errorf("等待设备 %s 就绪失败(%s): %w", deviceID, reason, err)
	}
	return m.enableRuntime(ctx, runtimeEnableRequest{
		DeviceID:     deviceID,
		OverrideEPDG: overrideEPDG,
		Generation:   generation,
		Reason:       reason,
	})
}

func (m *Manager) ensureLifecycleGeneration(deviceID string, generation uint64, reason string) uint64 {
	if generation != 0 {
		return generation
	}
	fallback := m.NextLifecycleGeneration(deviceID)
	logger.Debug("VoWiFi 启动缺少 lifecycle generation，已使用 fallback generation", "device", strings.TrimSpace(deviceID), "reason", strings.TrimSpace(reason), "generation", fallback)
	return fallback
}

func hostContext(ctx context.Context, adapter Adapter) context.Context {
	if ctx != nil {
		return ctx
	}
	if adapter != nil {
		if adapterCtx := adapter.Context(); adapterCtx != nil {
			return adapterCtx
		}
	}
	return context.Background()
}
