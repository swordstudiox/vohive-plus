package device

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

type vowifiLockBackendStub struct {
	mu           sync.RWMutex
	mode         string
	imsi         string
	imei         string
	imsiErr      error
	getIMSIDelay time.Duration
	imsiCalls    atomic.Int32
}

func (s *vowifiLockBackendStub) setMode(mode string) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *vowifiLockBackendStub) GetIMEI(ctx context.Context) (string, error) { return s.imei, nil }
func (s *vowifiLockBackendStub) GetIMSI(ctx context.Context) (string, error) {
	s.imsiCalls.Add(1)
	if s.getIMSIDelay > 0 {
		time.Sleep(s.getIMSIDelay)
	}
	if s.imsiErr != nil {
		return "", s.imsiErr
	}
	return s.imsi, nil
}
func (s *vowifiLockBackendStub) GetIMSILive(ctx context.Context) (string, error) {
	return s.GetIMSI(ctx)
}
func (s *vowifiLockBackendStub) GetICCID(ctx context.Context) (string, error) { return "", nil }
func (s *vowifiLockBackendStub) GetICCIDLive(ctx context.Context) (string, error) {
	return s.GetICCID(ctx)
}
func (s *vowifiLockBackendStub) GetMSISDN(ctx context.Context) (string, error)   { return "", nil }
func (s *vowifiLockBackendStub) GetRevision(ctx context.Context) (string, error) { return "", nil }
func (s *vowifiLockBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *vowifiLockBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *vowifiLockBackendStub) IsSimInserted(ctx context.Context) (bool, error) { return true, nil }
func (s *vowifiLockBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "", "", nil
}

func (s *vowifiLockBackendStub) GetNativeSPN(ctx context.Context) (string, error) {
	return "", nil
}
func (s *vowifiLockBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *vowifiLockBackendStub) SendSMS(ctx context.Context, to, body string) error { return nil }
func (s *vowifiLockBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *vowifiLockBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *vowifiLockBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *vowifiLockBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *vowifiLockBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	return nil
}
func (s *vowifiLockBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (s *vowifiLockBackendStub) Reboot(ctx context.Context) error { return nil }
func (s *vowifiLockBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	return 0, nil
}
func (s *vowifiLockBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	return nil
}
func (s *vowifiLockBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *vowifiLockBackendStub) Mode() string {
	s.mu.RLock()
	mode := s.mode
	s.mu.RUnlock()
	if mode == "" {
		return backend.BackendAT
	}
	return mode
}
func (s *vowifiLockBackendStub) Close() error { return nil }

func TestEnableVoWiFiBlockedWhenESIMSwitching(t *testing.T) {
	p := NewPool(&config.Config{})
	p.switchMu.Lock()
	p.switchingDevices["dev-1"] = true
	p.switchMu.Unlock()

	err := p.EnableVoWiFi("dev-1")
	if err == nil {
		t.Fatal("EnableVoWiFi expected error when eSIM switching, got nil")
	}
	if !strings.Contains(err.Error(), "正在切卡") {
		t.Fatalf("EnableVoWiFi error = %v, want contains %q", err, "正在切卡")
	}
}

func TestEnableVoWiFiDoesNotBlockReadPathsDuringInFlight(t *testing.T) {
	p := NewPool(&config.Config{})
	worker := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1"},
		Modem:   &modem.Manager{}, // 仅用于走 w.Modem!=nil 分支，实际不会被调用
		Backend: &vowifiLockBackendStub{mode: backend.BackendAT, imsi: "", imei: "861234567890123", getIMSIDelay: 600 * time.Millisecond},
	}
	p.mu.Lock()
	p.workers["dev-1"] = worker
	p.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- p.EnableVoWiFi("dev-1")
	}()

	waitDeadline := time.After(2 * time.Second)
	for {
		if p.voWiFiRuntimeStore().Starting("dev-1") {
			break
		}
		select {
		case <-waitDeadline:
			t.Fatal("EnableVoWiFi did not enter in-flight state")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	assertReturnsQuickly := func(name string, fn func()) {
		t.Helper()
		callDone := make(chan struct{}, 1)
		go func() {
			fn()
			callDone <- struct{}{}
		}()
		select {
		case <-callDone:
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("%s blocked too long", name)
		}
	}

	assertReturnsQuickly("IsVoWiFiActive", func() {
		_ = p.IsVoWiFiActive("dev-1")
	})
	assertReturnsQuickly("GetVoWiFiObs", func() {
		_ = p.GetVoWiFiObs("dev-1")
	})
	assertReturnsQuickly("GetVoWiFiRuntimeState", func() {
		_, _ = p.GetVoWiFiRuntimeState("dev-1")
	})

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("EnableVoWiFi expected error, got nil")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("EnableVoWiFi did not finish in time")
	}

	if p.voWiFiRuntimeStore().Starting("dev-1") {
		t.Fatal("VoWiFi runtime starting flag should be cleared on exit")
	}
}

func TestGetVoWiFiRuntimeStateWithoutAppReturnsFalse(t *testing.T) {
	p := NewPool(&config.Config{})
	if _, ok := p.GetVoWiFiRuntimeState("missing"); ok {
		t.Fatal("expected no runtime state for missing app")
	}
}

func TestGetVoWiFiRuntimeStateReturnsStartupStateBeforeAppIsActive(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-starting"
	want := runtimehost.State{
		DeviceID:   deviceID,
		LastReason: "正在检测 Modem 存活性...",
	}

	p.voWiFiRuntimeStore().RecordStartupState(deviceID, want)

	got, ok := p.GetVoWiFiRuntimeState(deviceID)
	if !ok {
		t.Fatal("expected startup runtime state to be visible")
	}
	if got.DeviceID != want.DeviceID || got.LastReason != want.LastReason {
		t.Fatalf("runtime state = %+v, want %+v", got, want)
	}
	if p.IsVoWiFiActive(deviceID) {
		t.Fatal("startup state must not make VoWiFi active")
	}
}

func TestRecordVoWiFiStartupStateCachesAndBroadcastsWhileInactive(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-startup-broadcast"
	ch, unsub := p.SubscribeVoWiFiState(deviceID)
	defer unsub()

	state := runtimehost.State{
		DeviceID:   deviceID,
		LastReason: "正在读取 SIM 卡信息 (IMSI/PLMN)...",
	}

	p.recordVoWiFiStartupState(deviceID, state)

	got, ok := p.GetVoWiFiRuntimeState(deviceID)
	if !ok {
		t.Fatal("expected startup runtime state")
	}
	if got.LastReason != state.LastReason {
		t.Fatalf("LastReason = %q, want %q", got.LastReason, state.LastReason)
	}
	if p.IsVoWiFiActive(deviceID) {
		t.Fatal("recording startup state must not activate VoWiFi")
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected startup state broadcast")
	}
}

func TestNewVoWiFiSIMReadyStartupStateUsesSIMPhase(t *testing.T) {
	now := time.Date(2026, 6, 1, 20, 30, 0, 0, time.UTC)
	got := newVoWiFiSIMReadyStartupState("dev-sim", "userspace", "LTE", now)

	if got.Phase != runtimehost.PhaseSIMReady {
		t.Fatalf("Phase = %q, want %q", got.Phase, runtimehost.PhaseSIMReady)
	}
	if !got.SIMReady {
		t.Fatalf("SIMReady=false, state=%+v", got)
	}
	if got.LastReason != "sim_ready" {
		t.Fatalf("LastReason = %q, want sim_ready", got.LastReason)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}
}

func TestRecordVoWiFiStartupStateKeepsNewestState(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-startup-newest"
	ch, unsub := p.SubscribeVoWiFiState(deviceID)
	defer unsub()
	earlier := runtimehost.State{
		DeviceID:   deviceID,
		LastReason: "正在读取 SIM 卡信息 (IMSI/PLMN)...",
		UpdatedAt:  time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
	}
	later := runtimehost.State{
		DeviceID:   deviceID,
		LastReason: "启动前置条件满足",
		UpdatedAt:  time.Date(2026, 4, 25, 10, 0, 1, 0, time.UTC),
	}

	p.recordVoWiFiStartupState(deviceID, later)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected broadcast for newer startup state")
	}
	p.recordVoWiFiStartupState(deviceID, earlier)
	select {
	case <-ch:
		t.Fatal("older startup state should not broadcast")
	case <-time.After(100 * time.Millisecond):
	}

	got, ok := p.GetVoWiFiRuntimeState(deviceID)
	if !ok {
		t.Fatal("expected startup runtime state")
	}
	if got.LastReason != later.LastReason {
		t.Fatalf("LastReason = %q, want newest %q", got.LastReason, later.LastReason)
	}
}

func TestClearVoWiFiStartupStateKeepsActiveAppAuthoritative(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-startup-success"
	activeApp := &runtimehost.Instance{}

	p.voWiFiRuntimeStore().RecordStartupState(deviceID, runtimehost.State{DeviceID: deviceID, LastReason: "starting", UpdatedAt: time.Now()})
	p.voWiFiRuntimeStore().SetInstance(deviceID, activeApp)

	p.clearVoWiFiStartupState(deviceID)

	if state, ok := p.GetVoWiFiRuntimeState(deviceID); !ok || state.LastReason == "starting" {
		t.Fatal("startup state should be cleared after promotion")
	}
	if !p.IsVoWiFiActive(deviceID) {
		t.Fatal("active app should remain after clearing startup state")
	}
	if _, ok := p.GetVoWiFiRuntimeState(deviceID); !ok {
		t.Fatal("active runtime state should remain visible")
	}
}

func TestClearVoWiFiStartupStateRemovesFailedStartupState(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-startup-failure"

	p.voWiFiRuntimeStore().RecordStartupState(deviceID, runtimehost.State{DeviceID: deviceID, LastReason: "ePDG 隧道建立失败", UpdatedAt: time.Now()})

	p.clearVoWiFiStartupState(deviceID)

	if _, ok := p.GetVoWiFiRuntimeState(deviceID); ok {
		t.Fatal("failed startup state should be cleared")
	}
	if p.IsVoWiFiActive(deviceID) {
		t.Fatal("failed startup must not activate VoWiFi")
	}
}

func TestClearVoWiFiStartupStateAndBroadcastNotifiesSubscribers(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-startup-failure-broadcast"
	ch, unsub := p.SubscribeVoWiFiState(deviceID)
	defer unsub()

	p.voWiFiRuntimeStore().RecordStartupState(deviceID, runtimehost.State{DeviceID: deviceID, LastReason: "ePDG 隧道建立失败", UpdatedAt: time.Now()})

	p.clearVoWiFiStartupStateAndBroadcast(deviceID)

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected startup state cleanup broadcast")
	}
	if _, ok := p.GetVoWiFiRuntimeState(deviceID); ok {
		t.Fatal("startup state should be cleared after cleanup broadcast")
	}
}

func TestStopVoWiFiAppForTeardownBroadcastsState(t *testing.T) {
	p := NewPool(&config.Config{})
	ch, unsub := p.SubscribeVoWiFiState("dev-2")
	defer unsub()

	p.voWiFiRuntimeStore().SetInstance("dev-2", &runtimehost.Instance{})

	_ = p.stopVoWiFiAppForTeardown(context.Background(), "dev-2", "test")

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected state broadcast after teardown")
	}
}

func TestSubscribeVoWiFiStateUnsubRemovesEmptyBucket(t *testing.T) {
	p := NewPool(&config.Config{})
	_, unsub := p.SubscribeVoWiFiState("dev-clean")

	unsub()

	if got := p.voWiFiHost().SubscriberCount("dev-clean"); got != 0 {
		t.Fatalf("expected empty device subscription bucket to be removed, got %d subscribers", got)
	}
}

func TestWaitWorkerReadyReturnsWhenWorkerBecomesHealthy(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-worker-ready"
	backendStub := &vowifiLockBackendStub{mode: backend.BackendQMI}
	worker := &Worker{ID: deviceID, Config: config.DeviceConfig{ID: deviceID}, Backend: backendStub}
	p.mu.Lock()
	p.workers[deviceID] = worker
	p.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.waitWorkerReady(deviceID, 2*time.Second)
	}()

	time.Sleep(150 * time.Millisecond)
	backendStub.setMode(backend.BackendAT)
	worker.Modem = &modem.Manager{}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("waitWorkerReady() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitWorkerReady() did not return after worker became healthy")
	}
}

func TestWaitWorkerReadyTimeoutWhenWorkerMissing(t *testing.T) {
	p := NewPool(&config.Config{})
	err := p.waitWorkerReady("missing", 120*time.Millisecond)
	if err == nil {
		t.Fatal("waitWorkerReady() expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitWorkerReady() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestWaitRadioRecoveryReadyMissingWorker(t *testing.T) {
	p := NewPool(&config.Config{})
	err := p.waitRadioRecoveryReady("missing", 120*time.Millisecond)
	if err == nil {
		t.Fatal("waitRadioRecoveryReady() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("waitRadioRecoveryReady() error = %v, want contains 不存在", err)
	}
}
