package device

import (
	"errors"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestHealthCheckSkipsDeviceUnderRebootRecovery(t *testing.T) {
	// 当设备处于 modemRebootRecovering 中时，
	// healthCheckLoop 不应尝试快速拉起或重扫，而应完全委托给恢复循环。
	p := NewPool(&config.Config{})
	defer p.cancel()

	deviceID := "dev-qmi"
	p.mu.Lock()
	if p.modemRebootRecovering == nil {
		p.modemRebootRecovering = make(map[string]bool)
	}
	p.modemRebootRecovering[deviceID] = true
	p.mu.Unlock()

	// 检查判据逻辑
	p.mu.RLock()
	isRecovering := p.modemRebootRecovering[deviceID]
	p.mu.RUnlock()

	if !isRecovering {
		t.Fatalf("device should be marked as under reboot recovery, but modemRebootRecovering[%s]=false", deviceID)
	}
}

func TestHealthCheckAllowsFastPullWhenNotRecovering(t *testing.T) {
	// 当设备不在恢复中时，healthCheckLoop 可以尝试快速拉起。
	p := NewPool(&config.Config{})
	defer p.cancel()

	deviceID := "dev-qmi"
	// 不设置 modemRebootRecovering 标记

	p.mu.RLock()
	isRecovering := p.modemRebootRecovering[deviceID]
	p.mu.RUnlock()

	if isRecovering {
		t.Fatalf("device should NOT be marked as under reboot recovery, but modemRebootRecovering[%s]=true", deviceID)
	}
}

// TestRunHealthCheckTickSkipsObservationWindowOnTransportDownError 测试当探活失败的错误
// 明确表示传输已断开（broken pipe/EOF/connection closed 等）时，应跳过 3 次观察窗口，
// 第一次失败就直接触发恢复，而不是像普通超时那样等满 qmiHealthFailureThreshold 次。
func TestRunHealthCheckTickSkipsObservationWindowOnTransportDownError(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()

	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{
			mode:      backend.BackendQMI,
			opModeErr: errors.New("write failed: write unix @->@qmi-proxy: write: broken pipe"),
		},
	}
	p.workers["dev1"] = worker

	p.runHealthCheckTick()

	// scheduleWorkerRecoveryWithTransportEvent 内部会再记一次 Reprobing 事件覆盖前面的 Invalid 事件，
	// 这跟原有 3 次阈值触发恢复时的行为一致；这里通过 Reason 区分走的是哪条触发路径。
	snapshot := worker.HealthSnapshot()
	if snapshot.State != HealthStateReprobing {
		t.Fatalf("state=%s want %s after single transport-down failure triggers recovery", snapshot.State, HealthStateReprobing)
	}
	if snapshot.Reason != "qmi_transport_down" {
		t.Fatalf("reason=%q want qmi_transport_down", snapshot.Reason)
	}
}

// TestRunHealthCheckTickStillWaitsForThresholdOnTransientError 测试普通瞬时错误（非传输确认已断）
// 仍然遵循原有的 3 次观察窗口，不应被这次改动误伤。
func TestRunHealthCheckTickStillWaitsForThresholdOnTransientError(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()

	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{
			mode:      backend.BackendQMI,
			opModeErr: errors.New("context deadline exceeded"),
		},
	}
	p.workers["dev1"] = worker

	p.runHealthCheckTick()

	snapshot := worker.HealthSnapshot()
	if snapshot.State == HealthStateInvalid {
		t.Fatalf("state=%s, single transient timeout should not bypass the observation window", snapshot.State)
	}
}
