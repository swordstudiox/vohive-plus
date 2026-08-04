package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestHealthCheckSnapshotsWorkersBeforeQMIProbe(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()

	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	backendStub := &blockingHealthBackendStub{
		probeStarted: probeStarted,
		releaseProbe: releaseProbe,
	}
	worker := &Worker{
		ID:      "dev1",
		Config:  config.DeviceConfig{ID: "dev1", DeviceBackend: backend.BackendQMI, ControlDevice: "/dev/cdc-wdm0"},
		Backend: backendStub,
	}
	pool.workers["dev1"] = worker

	done := make(chan struct{})
	go func() {
		pool.runHealthCheckTick()
		close(done)
	}()

	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		t.Fatal("health probe did not start")
	}

	lockAcquired := make(chan struct{})
	go func() {
		pool.mu.Lock()
		pool.mu.Unlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(200 * time.Millisecond):
		close(releaseProbe)
		t.Fatal("pool mutex stayed blocked while QMI health probe was in flight")
	}

	close(releaseProbe)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health check tick did not finish")
	}
}

func TestWorkerRecordWatchdogEventTransitions(t *testing.T) {
	worker := &Worker{ID: "dev1"}

	snapshot := worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:               HealthLayerQMI,
		State:               HealthStateSuspect,
		Reason:              "qmi_health_probe_failed",
		ConsecutiveFailures: 1,
		Threshold:           qmiHealthFailureThreshold,
	})
	if snapshot.State != HealthStateSuspect {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateSuspect)
	}
	if snapshot.Layer != HealthLayerQMI {
		t.Fatalf("layer=%s want %s", snapshot.Layer, HealthLayerQMI)
	}
	if snapshot.ConsecutiveFailures != 1 {
		t.Fatalf("failures=%d want 1", snapshot.ConsecutiveFailures)
	}

	snapshot = worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:               HealthLayerQMI,
		State:               HealthStateInvalid,
		Reason:              "qmi_health_threshold",
		ConsecutiveFailures: qmiHealthFailureThreshold,
		Threshold:           qmiHealthFailureThreshold,
	})
	if snapshot.State != HealthStateInvalid {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateInvalid)
	}
	if worker.state.Meta.Healthy {
		t.Fatal("Meta.Healthy=true after invalid event, want false")
	}

	snapshot = worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:  HealthLayerPool,
		State:  HealthStateReprobing,
		Reason: "qmi_health_threshold",
	})
	if snapshot.State != HealthStateReprobing {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateReprobing)
	}

	snapshot = worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:  HealthLayerQMI,
		State:  HealthStateHealthy,
		Reason: "qmi_connected",
	})
	if snapshot.State != HealthStateHealthy {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateHealthy)
	}
	if !worker.state.Meta.Healthy {
		t.Fatal("Meta.Healthy=false after healthy event, want true")
	}

	snapshot = worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:  HealthLayerPool,
		State:  HealthStateFailed,
		Reason: "modem_reboot_recovery_exhausted",
	})
	if snapshot.State != HealthStateFailed {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateFailed)
	}
	if worker.state.Meta.Healthy {
		t.Fatal("Meta.Healthy=true after failed event, want false")
	}
}

func TestWorkerRecoveryWindowUsesUnifiedHealthSnapshot(t *testing.T) {
	worker := &Worker{ID: "dev1"}

	worker.markHealthRecoveryWindow(time.Minute)

	snapshot := worker.HealthSnapshot()
	if snapshot.State != HealthStateRecovering {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateRecovering)
	}
	if snapshot.Layer != HealthLayerQMI {
		t.Fatalf("layer=%s want %s", snapshot.Layer, HealthLayerQMI)
	}
	if snapshot.RecoveryUntil.IsZero() {
		t.Fatal("RecoveryUntil is zero")
	}
	if remaining := worker.healthRecoveryRemaining(time.Now()); remaining <= 0 {
		t.Fatalf("recovery remaining=%s want positive", remaining)
	}
}

func TestPoolScheduleWorkerRecoveryMarksReprobing(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()
	worker := &Worker{ID: "dev1"}
	pool.workers["dev1"] = worker

	pool.scheduleWorkerRecovery("dev1", "qmi_health_threshold")

	snapshot := worker.HealthSnapshot()
	if snapshot.State != HealthStateReprobing {
		t.Fatalf("state=%s want %s", snapshot.State, HealthStateReprobing)
	}
	if snapshot.Layer != HealthLayerPool {
		t.Fatalf("layer=%s want %s", snapshot.Layer, HealthLayerPool)
	}
	if snapshot.Reason != "qmi_health_threshold" {
		t.Fatalf("reason=%q want qmi_health_threshold", snapshot.Reason)
	}
}

func TestHandleRecoveryExhaustedSchedulesRecovery(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()
	worker := &Worker{ID: "dev1", stop: make(chan struct{})}
	pool.workers["dev1"] = worker

	if !pool.handleTransportRecoveryExhausted(worker, worker.generation, HealthLayerQMI, "device_removed", errors.New("no such file or directory")) {
		t.Fatal("handleTransportRecoveryExhausted() = false, want true")
	}
}

func TestQMIRecoveryExhaustedDuplicateIsSuppressedByController(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()
	worker := &Worker{ID: "dev1", stop: make(chan struct{})}
	pool.workers["dev1"] = worker
	err := errors.New("no such file or directory")

	if !pool.handleTransportRecoveryExhausted(worker, worker.generation, HealthLayerQMI, "device_removed", err) {
		t.Fatal("first exhausted = false, want true")
	}
	if pool.handleTransportRecoveryExhausted(worker, worker.generation, HealthLayerQMI, "device_removed", err) {
		t.Fatal("duplicate exhausted = true, want false")
	}
}

func TestQMIRecoveryExhaustedStaleGenerationIsIgnored(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()
	stale := &Worker{ID: "dev1", stop: make(chan struct{}), generation: 1}
	current := &Worker{ID: "dev1", stop: make(chan struct{}), generation: 2}
	pool.workers["dev1"] = current

	if pool.handleTransportRecoveryExhausted(stale, stale.generation, HealthLayerQMI, "recovery_exhausted", errors.New("x")) {
		t.Fatal("stale worker exhausted should be ignored")
	}
	if !pool.handleTransportRecoveryExhausted(current, current.generation, HealthLayerQMI, "recovery_exhausted", errors.New("x")) {
		t.Fatal("current worker exhausted should be handled")
	}
}

func TestProbeDeviceHealthReturnsQMIProbeError(t *testing.T) {
	probeErr := errors.New("write failed: write unix @->@qmi-proxy: write: broken pipe")
	worker := &Worker{
		ID: "dev1",
		Backend: &workerStatusBackendStub{
			mode:      backend.BackendQMI,
			opModeErr: probeErr,
		},
	}

	healthy, err := worker.ProbeDeviceHealth()

	if healthy {
		t.Fatal("healthy=true, want false")
	}
	if !errors.Is(err, probeErr) {
		t.Fatalf("err=%v want %v", err, probeErr)
	}
}

type blockingHealthBackendStub struct {
	probeStarted chan struct{}
	releaseProbe chan struct{}
}

func (s *blockingHealthBackendStub) GetIMEI(ctx context.Context) (string, error)      { return "", nil }
func (s *blockingHealthBackendStub) GetIMSI(ctx context.Context) (string, error)      { return "", nil }
func (s *blockingHealthBackendStub) GetIMSILive(ctx context.Context) (string, error)  { return "", nil }
func (s *blockingHealthBackendStub) GetICCID(ctx context.Context) (string, error)     { return "", nil }
func (s *blockingHealthBackendStub) GetMSISDN(ctx context.Context) (string, error)    { return "", nil }
func (s *blockingHealthBackendStub) GetICCIDLive(ctx context.Context) (string, error) { return "", nil }
func (s *blockingHealthBackendStub) GetRevision(ctx context.Context) (string, error)  { return "", nil }
func (s *blockingHealthBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) IsSimInserted(ctx context.Context) (bool, error) {
	return true, nil
}
func (s *blockingHealthBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "", "", nil
}
func (s *blockingHealthBackendStub) GetNativeSPN(ctx context.Context) (string, error) {
	return "", nil
}
func (s *blockingHealthBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) GetSIMMetadataLive(ctx context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) SendSMS(ctx context.Context, to, body string) error { return nil }
func (s *blockingHealthBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *blockingHealthBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *blockingHealthBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *blockingHealthBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	return nil
}
func (s *blockingHealthBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	select {
	case <-s.probeStarted:
	default:
		close(s.probeStarted)
	}
	select {
	case <-s.releaseProbe:
		return backend.ModeOnline, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func (s *blockingHealthBackendStub) Reboot(ctx context.Context) error { return nil }
func (s *blockingHealthBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	return 0, nil
}
func (s *blockingHealthBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	return nil
}
func (s *blockingHealthBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *blockingHealthBackendStub) ResolveSIMAuthAID(ctx context.Context, app string, fallbackAID string) (string, string, error) {
	return fallbackAID, "fallback_test", nil
}
func (s *blockingHealthBackendStub) Mode() string { return backend.BackendQMI }
func (s *blockingHealthBackendStub) Close() error { return nil }
